package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/n3wscott/phonebook/internal/model"
	"github.com/n3wscott/phonebook/internal/testutil"
)

func TestPhonebookHandlerETagAndCaching(t *testing.T) {
	logger := testutil.NewTestLogger()
	srv := NewServer(Config{Addr: ":0", BasePath: "/xml/", AllowDebug: true}, logger)

	contact := model.Contact{FirstName: "John", LastName: "Doe", Phone: "8000", AccountIndex: 1}
	xml := []byte("<?xml version=\"1.0\"?><AddressBook></AddressBook>")
	lastMod := time.Unix(1700000000, 0).UTC()
	srv.Update([]model.Contact{contact}, xml, lastMod)

	handler := srv.Handler()

	req := httptest.NewRequest(http.MethodGet, "/xml/phonebook.xml", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	etag := rr.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("missing ETag header")
	}
	lastModified := rr.Header().Get("Last-Modified")
	if lastModified == "" {
		t.Fatalf("missing Last-Modified header")
	}

	// If-None-Match should trigger 304
	req = httptest.NewRequest(http.MethodGet, "/xml/phonebook.xml", nil)
	req.Header.Set("If-None-Match", etag)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotModified {
		t.Fatalf("expected 304 for matching ETag, got %d", rr.Code)
	}

	// If-Modified-Since should also trigger 304
	req = httptest.NewRequest(http.MethodGet, "/xml/phonebook.xml", nil)
	req.Header.Set("If-Modified-Since", lastModified)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotModified {
		t.Fatalf("expected 304 for If-Modified-Since, got %d", rr.Code)
	}
}

func TestHealthEndpoint(t *testing.T) {
	logger := testutil.NewTestLogger()
	srv := NewServer(Config{Addr: ":0", BasePath: "/", AllowDebug: false}, logger)
	srv.Update([]model.Contact{}, []byte("<AddressBook></AddressBook>"), time.Unix(0, 0))

	handler := srv.Handler()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var body struct {
		OK       bool   `json:"ok"`
		Contacts int    `json:"contacts"`
		Version  uint64 `json:"version"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if !body.OK {
		t.Fatalf("expected ok=true")
	}
}
