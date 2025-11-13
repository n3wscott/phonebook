package integration_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/n3wscott/phonebook/internal/httpapi"
	"github.com/n3wscott/phonebook/internal/load"
	"github.com/n3wscott/phonebook/internal/testutil"
	"github.com/n3wscott/phonebook/internal/xmlgen"
)

func TestHotReloadFlow(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "contacts.yaml")
	writeFile(t, file, `- first_name: Alpha
  last_name: Tester
  phone: "1000"
  account_index: 1
`)

	logger := testutil.NewTestLogger()
	loader := load.New(dir, logger)
	srv := httpapi.NewServer(httpapi.Config{Addr: ":0", BasePath: "/xml/"}, logger)

	rebuild(t, loader, srv)

	handler := srv.Handler()
	req := httptest.NewRequest(http.MethodGet, "/xml/phonebook.xml", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Alpha") {
		t.Fatalf("expected contact Alpha in response")
	}
	etag := rr.Header().Get("ETag")
	if etag == "" {
		t.Fatalf("missing ETag")
	}

	time.Sleep(1100 * time.Millisecond)
	writeFile(t, file, `- first_name: Beta
  last_name: Tester
  phone: "1000"
  account_index: 1
`)
	rebuild(t, loader, srv)

	req = httptest.NewRequest(http.MethodGet, "/xml/phonebook.xml", nil)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Beta") {
		t.Fatalf("expected updated contact Beta in response")
	}
	if rr.Header().Get("ETag") == etag {
		t.Fatalf("expected ETag to change after reload")
	}
}

func rebuild(t *testing.T, loader *load.Loader, srv *httpapi.Server) {
	t.Helper()
	res, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	xml, err := xmlgen.Build(res.Contacts)
	if err != nil {
		t.Fatalf("xml build error = %v", err)
	}
	srv.Update(res.Contacts, xml, res.LastModified())
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}
