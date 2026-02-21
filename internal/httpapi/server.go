package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/n3wscott/phonebook/internal/calls"
	"github.com/n3wscott/phonebook/internal/model"
)

// Server exposes phonebook HTTP endpoints.
type Server struct {
	addr       string
	basePath   string
	tlsCert    string
	tlsKey     string
	allowDebug bool
	logger     Logger
	calls      *calls.Service

	mu       sync.RWMutex
	snapshot snapshot
	version  uint64
	httpSrv  *http.Server
}

// Logger abstracts the log methods used here.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Debug(msg string, args ...any)
}

// Config bundles HTTP server options.
type Config struct {
	Addr        string
	BasePath    string
	TLSCert     string
	TLSKey      string
	AllowDebug  bool
	CallService *calls.Service
}

// snapshot contains the data served to clients.
type snapshot struct {
	XML          []byte
	Contacts     []model.Contact
	ContactCount int
	ETag         string
	LastModified time.Time
}

// New creates a server with the supplied configuration.
func New(cfg Config, logger Logger) *Server {
	return &Server{
		addr:       cfg.Addr,
		basePath:   cfg.BasePath,
		tlsCert:    cfg.TLSCert,
		tlsKey:     cfg.TLSKey,
		allowDebug: cfg.AllowDebug,
		logger:     logger,
		calls:      cfg.CallService,
	}
}

// NewServer is a convenience alias.
func NewServer(cfg Config, logger Logger) *Server {
	return New(cfg, logger)
}

// Handler exposes the HTTP handler for use in tests.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(s.join("phonebook.xml"), s.handlePhonebook)
	mux.HandleFunc(s.join("healthz"), s.handleHealthz)
	if s.calls != nil {
		mux.HandleFunc(s.join("calls"), s.handleCallsPage)
		mux.HandleFunc(s.join("calls/ws"), s.handleCallsWS)
		mux.HandleFunc(s.join("api/calls/active"), s.handleCallsActive)
		mux.HandleFunc(s.join("api/calls/history"), s.handleCallsHistory)
		mux.HandleFunc(s.join("api/calls/contacts"), s.handleCallsContacts)
	}
	if s.allowDebug {
		mux.HandleFunc(s.join("debug"), s.handleDebug)
	}
	return mux
}

// Start launches the HTTP server and blocks until it exits.
func (s *Server) Start(ctx context.Context) error {
	handler := s.Handler()

	srv := &http.Server{
		Addr:    s.addr,
		Handler: handler,
	}
	s.httpSrv = srv

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	s.logger.Info("serving", "addr", s.addr, "basePath", s.basePath)

	if s.tlsCert != "" && s.tlsKey != "" {
		return srv.ListenAndServeTLS(s.tlsCert, s.tlsKey)
	}
	return srv.ListenAndServe()
}

// Update replaces the snapshot and bumps the version counter.
func (s *Server) Update(contacts []model.Contact, xml []byte, lastModified time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if lastModified.IsZero() {
		lastModified = time.Now().UTC()
	}
	etag := etagFor(xml)
	s.snapshot = snapshot{
		XML:          append([]byte(nil), xml...),
		Contacts:     append([]model.Contact(nil), contacts...),
		ContactCount: len(contacts),
		ETag:         etag,
		LastModified: lastModified.UTC().Round(time.Second),
	}
	s.version++
}

func (s *Server) currentSnapshot() (snapshot, uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot, s.version
}

// Stats returns the current contact count and version number.
func (s *Server) Stats() (int, uint64) {
	snap, version := s.currentSnapshot()
	return snap.ContactCount, version
}

func (s *Server) handlePhonebook(w http.ResponseWriter, r *http.Request) {
	snap, _ := s.currentSnapshot()
	if len(snap.XML) == 0 {
		http.Error(w, "phonebook not ready", http.StatusServiceUnavailable)
		return
	}

	if match := r.Header.Get("If-None-Match"); match != "" && match == snap.ETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	if ims := r.Header.Get("If-Modified-Since"); ims != "" {
		if t, err := http.ParseTime(ims); err == nil {
			if !snap.LastModified.After(t) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("ETag", snap.ETag)
	w.Header().Set("Last-Modified", snap.LastModified.UTC().Format(http.TimeFormat))
	_, _ = w.Write(snap.XML)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	snap, version := s.currentSnapshot()
	payload := map[string]any{
		"ok":       len(snap.XML) > 0,
		"contacts": snap.ContactCount,
		"version":  version,
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) handleDebug(w http.ResponseWriter, r *http.Request) {
	snap, version := s.currentSnapshot()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<html><body><h1>Contacts (v%[1]d)</h1><ul>", version)
	for _, c := range snap.Contacts {
		phone := ""
		if len(c.Phones) > 0 {
			phone = c.Phones[0].Number
		}
		fmt.Fprintf(w, "<li>%s %s &ndash; ext %s (%s) &mdash; %s</li>",
			escapeHTML(c.FirstName),
			escapeHTML(c.LastName),
			escapeHTML(c.Extension),
			escapeHTML(phone),
			escapeHTML(c.SourcePath))
	}
	fmt.Fprintf(w, "</ul></body></html>")
}

func (s *Server) join(rel string) string {
	if s.basePath == "/" {
		return "/" + rel
	}
	return s.basePath + rel
}

func etagFor(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("\"%s\"", hex.EncodeToString(sum[:]))
}

func escapeHTML(input string) string {
	return htmlReplacer.Replace(input)
}

var htmlReplacer = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	"\"", "&quot;",
	"'", "&#39;",
)
