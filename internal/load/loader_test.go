package load_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/n3wscott/phonebook/internal/config"
	"github.com/n3wscott/phonebook/internal/load"
	"github.com/n3wscott/phonebook/internal/testutil"
)

func TestLoaderParsesContacts(t *testing.T) {
	root := t.TempDir()
	writeContactFile(t, root, "contacts/team.yaml", `contacts:
  - id: alpha
    first_name: Alpha
    last_name: Tester
    ext: "1000"
    password: "pw1"
    account_index: 2
    group_id: 1
  - id: bravo
    first_name: Bravo
    last_name: Tester
    ext: "1001"
    password: "pw2"
    phones:
      - number: " 6 0 0 0 "
        account_index: 3
`)
	cfg, defs := testConfig()
	loader := load.New(root, testutil.NewTestLogger())
	res, err := loader.LoadContacts(cfg, defs)
	if err != nil {
		t.Fatalf("LoadContacts() error = %v", err)
	}
	if len(res.Contacts) != 2 {
		t.Fatalf("expected 2 contacts, got %d", len(res.Contacts))
	}
	alpha := res.Contacts[0]
	if alpha.Extension != "1000" || alpha.Auth.Username != "1000" {
		t.Fatalf("unexpected alpha contact: %+v", alpha)
	}
	if got := alpha.Phones[0].AccountIndex; got != 2 {
		t.Fatalf("expected fallback account index 2, got %d", got)
	}
	bravo := res.Contacts[1]
	if bravo.Phones[0].Number != "6000" {
		t.Fatalf("expected normalized phone 6000, got %s", bravo.Phones[0].Number)
	}
}

func TestLoaderDedupLastWins(t *testing.T) {
	root := t.TempDir()
	writeContactFile(t, root, "contacts/a.yaml", `- id: jane
  first_name: Jane
  last_name: Roe
  ext: "200"
  password: "pw"
`)
	writeContactFile(t, root, "contacts/z.yaml", `- id: jane
  first_name: Jane
  last_name: Roe
  ext: "200"
  password: "pw"
  account_index: 3
`)
	cfg, defs := testConfig()
	loader := load.New(root, testutil.NewTestLogger())
	res, err := loader.LoadContacts(cfg, defs)
	if err != nil {
		t.Fatalf("LoadContacts() error = %v", err)
	}
	if len(res.Contacts) != 1 {
		t.Fatalf("expected 1 contact, got %d", len(res.Contacts))
	}
	if got := res.Contacts[0].AccountIndex; got == nil || *got != 3 {
		t.Fatalf("expected later file to win account_index, got %#v", res.Contacts[0].AccountIndex)
	}
}

func TestLoaderSkipsInvalid(t *testing.T) {
	root := t.TempDir()
	writeContactFile(t, root, "contacts/users.yaml", `contacts:
  - id: bad
    first_name: Bad
    last_name: Actor
    ext: "abc!"
    password: "pw"
`)
	cfg, defs := testConfig()
	loader := load.New(root, testutil.NewTestLogger())
	res, err := loader.LoadContacts(cfg, defs)
	if err != nil {
		t.Fatalf("LoadContacts() error = %v", err)
	}
	if len(res.Contacts) != 0 {
		t.Fatalf("expected no valid contacts, got %d", len(res.Contacts))
	}
}

func writeContactFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func testConfig() (config.Config, config.Defaults) {
	cfg := config.Config{
		EndpointTemplates: []config.EndpointConfig{
			{Name: "endpoint-template", Extra: map[string]any{"context": "internal"}},
		},
	}
	defs := config.Defaults{
		AOR: config.AORDefaults{
			MaxContacts:      1,
			RemoveExisting:   true,
			QualifyFrequency: 30,
		},
		Auth: config.AuthDefaults{UsernameEqualsExt: true},
		Endpoint: config.EndpointDefaults{
			Template: "endpoint-template",
		},
	}
	return cfg, defs
}
