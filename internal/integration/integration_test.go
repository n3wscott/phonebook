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
	"github.com/n3wscott/phonebook/internal/project"
	"github.com/n3wscott/phonebook/internal/testutil"
)

func TestHotReloadFlow(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir)
	contactsDir := filepath.Join(dir, "contacts")
	if err := os.MkdirAll(contactsDir, 0o755); err != nil {
		t.Fatalf("mkdir contacts: %v", err)
	}
	file := filepath.Join(contactsDir, "users.yaml")
	writeFile(t, file, `contacts:
  - id: alpha
    first_name: Alpha
    last_name: Tester
    ext: "1000"
    password: "pw1"
    account_index: 1
`)

	logger := testutil.NewTestLogger()
	builder := &project.Builder{Dir: dir, Logger: logger}
	state := buildState(t, builder)

	srv := httpapi.NewServer(httpapi.Config{Addr: ":0", BasePath: "/xml/"}, logger)
	srv.Update(state.Contacts, state.Phonebook, state.LastUpdate)

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

	time.Sleep(10 * time.Millisecond)
	writeFile(t, file, `contacts:
  - id: beta
    first_name: Beta
    last_name: Tester
    ext: "1000"
    password: "pw1"
    account_index: 1
`)
	state = buildState(t, builder)
	srv.Update(state.Contacts, state.Phonebook, state.LastUpdate)

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

func writeConfig(t *testing.T, dir string) {
	t.Helper()
	cfg := `global:
  user_agent: "TestAgent"

transports:
  - name: "transport-udp"
    protocol: "udp"
    bind: "0.0.0.0"

endpoint_templates:
  - name: "endpoint-template"
    context: "internal"
    disallow: ["all"]
    allow: ["ulaw"]

dialplan:
  context: "internal"
`
	def := `endpoint:
  template: "endpoint-template"
auth:
  username_equals_ext: true
aor:
  max_contacts: 1
  remove_existing: true
  qualify_frequency: 30
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "defaults.yaml"), []byte(def), 0o644); err != nil {
		t.Fatalf("write defaults: %v", err)
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

func buildState(t *testing.T, builder *project.Builder) project.State {
	t.Helper()
	state, err := builder.Build()
	if err != nil {
		t.Fatalf("builder.Build() error = %v", err)
	}
	return state
}
