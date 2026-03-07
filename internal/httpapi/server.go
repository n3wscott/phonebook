package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
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
	tr069    tr069Stats
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
	XML            []byte
	Contacts       []model.Contact
	Provision      map[string][]byte
	ContactCount   int
	ProvisionCount int
	ETag           string
	LastModified   time.Time
}

type tr069Stats struct {
	Count      uint64
	LastSeen   time.Time
	LastSerial string
	LastOUI    string
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
	mux.HandleFunc("/prov/", s.handleProvision)
	mux.HandleFunc("/tr069", s.handleTR069)
	if s.basePath != "/" {
		mux.HandleFunc(s.join("prov/"), s.handleProvision)
	}
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

// Update replaces the XML/contact snapshot and bumps the version counter.
func (s *Server) Update(contacts []model.Contact, xml []byte, lastModified time.Time) {
	s.UpdateProvision(contacts, xml, nil, lastModified)
}

// UpdateProvision replaces XML/contact/provisioning snapshots and bumps version.
func (s *Server) UpdateProvision(contacts []model.Contact, xml []byte, provision map[string][]byte, lastModified time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if lastModified.IsZero() {
		lastModified = time.Now().UTC()
	}
	etag := etagFor(xml)
	provCopy := cloneProvision(provision)
	s.snapshot = snapshot{
		XML:            append([]byte(nil), xml...),
		Contacts:       append([]model.Contact(nil), contacts...),
		Provision:      provCopy,
		ContactCount:   len(contacts),
		ProvisionCount: len(provCopy),
		ETag:           etag,
		LastModified:   lastModified.UTC().Round(time.Second),
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

func (s *Server) handleProvision(w http.ResponseWriter, r *http.Request) {
	snap, _ := s.currentSnapshot()
	if len(snap.Provision) == 0 {
		http.NotFound(w, r)
		return
	}

	name := provisionNameFromPath(r.URL.Path, s.basePath)
	if name == "" {
		http.NotFound(w, r)
		return
	}
	payload, ok := snap.Provision[name]
	if !ok {
		payload, ok = snap.Provision[strings.ToLower(name)]
		if !ok {
			http.NotFound(w, r)
			return
		}
	}

	if strings.HasSuffix(strings.ToLower(name), ".xml") {
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(payload)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	snap, version := s.currentSnapshot()
	s.mu.RLock()
	tr069 := s.tr069
	s.mu.RUnlock()
	payload := map[string]any{
		"ok":                len(snap.XML) > 0,
		"contacts":          snap.ContactCount,
		"provision_files":   snap.ProvisionCount,
		"tr069_count":       tr069.Count,
		"tr069_last_seen":   tr069.LastSeen.UTC().Format(time.RFC3339),
		"tr069_last_oui":    tr069.LastOUI,
		"tr069_last_serial": tr069.LastSerial,
		"version":           version,
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) handleTR069(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	serial := firstMatch(xmlSerialRe, body)
	oui := firstMatch(xmlOUIRe, body)
	product := firstMatch(xmlProductClassRe, body)
	eventCodes := allMatches(xmlEventCodeRe, body)
	isInform := bytesContainsAny(body, "cwmp:Inform", ":Inform")

	s.mu.Lock()
	s.tr069.Count++
	s.tr069.LastSeen = time.Now().UTC()
	s.tr069.LastSerial = serial
	s.tr069.LastOUI = oui
	s.mu.Unlock()

	s.logger.Info("tr069 heartbeat", "remote", r.RemoteAddr, "serial", serial, "oui", oui, "product", product, "events", strings.Join(eventCodes, ","), "bytes", len(body), "inform", isInform)

	if isInform {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(informResponseXML))
		return
	}

	w.WriteHeader(http.StatusOK)
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
	fmt.Fprintf(w, "</ul><p>Provisioning files: %d</p></body></html>", snap.ProvisionCount)
}

func (s *Server) join(rel string) string {
	if s.basePath == "/" {
		return "/" + rel
	}
	return s.basePath + rel
}

func provisionNameFromPath(path, basePath string) string {
	name := ""
	if strings.HasPrefix(path, "/prov/") {
		name = strings.TrimPrefix(path, "/prov/")
	} else if basePath != "/" {
		prefix := basePath + "prov/"
		if strings.HasPrefix(path, prefix) {
			name = strings.TrimPrefix(path, prefix)
		}
	}
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return ""
	}
	return name
}

func cloneProvision(in map[string][]byte) map[string][]byte {
	if len(in) == 0 {
		return map[string][]byte{}
	}
	out := make(map[string][]byte, len(in))
	for k, v := range in {
		out[k] = append([]byte(nil), v...)
	}
	return out
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

var (
	xmlSerialRe       = regexp.MustCompile(`<SerialNumber>([^<]+)</SerialNumber>`)
	xmlOUIRe          = regexp.MustCompile(`<OUI>([^<]+)</OUI>`)
	xmlProductClassRe = regexp.MustCompile(`<ProductClass>([^<]+)</ProductClass>`)
	xmlEventCodeRe    = regexp.MustCompile(`<EventCode>([^<]+)</EventCode>`)
)

const informResponseXML = `<?xml version="1.0" encoding="UTF-8"?>
<soap-env:Envelope xmlns:soap-env="http://schemas.xmlsoap.org/soap/envelope/" xmlns:cwmp="urn:dslforum-org:cwmp-1-0">
  <soap-env:Header/>
  <soap-env:Body>
    <cwmp:InformResponse>
      <MaxEnvelopes>1</MaxEnvelopes>
    </cwmp:InformResponse>
  </soap-env:Body>
</soap-env:Envelope>
`

func firstMatch(re *regexp.Regexp, body []byte) string {
	m := re.FindSubmatch(body)
	if len(m) != 2 {
		return ""
	}
	return string(m[1])
}

func allMatches(re *regexp.Regexp, body []byte) []string {
	matches := re.FindAllSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) == 2 {
			out = append(out, string(m[1]))
		}
	}
	return out
}

func bytesContainsAny(body []byte, needles ...string) bool {
	s := string(body)
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
