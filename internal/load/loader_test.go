package load_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n3wscott/phonebook/internal/load"
	"github.com/n3wscott/phonebook/internal/testutil"
)

func TestLoaderParsesMultipleFormats(t *testing.T) {
	dir := t.TempDir()

	listFile := filepath.Join(dir, "list.yaml")
	if err := os.WriteFile(listFile, []byte(`- first_name: John
  last_name: Doe
  phone: "8000"
  account_index: 1
  group_id: 0
`), 0o644); err != nil {
		t.Fatal(err)
	}

	nestedDir := filepath.Join(dir, "team")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	objectFile := filepath.Join(nestedDir, "eng.yaml")
	if err := os.WriteFile(objectFile, []byte(`contacts:
  - first_name: Lily
    last_name: Lee
    phone: " 6 0 0 0 "
    account_index: 2
    group_id: 2
  - first_name: Amir
    last_name: Khan
    phone: "+1 555 1000"
    account_index: 3
`), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := load.New(dir, testutil.NewTestLogger())
	res, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := len(res.Contacts); got != 3 {
		t.Fatalf("expected 3 contacts, got %d", got)
	}

	// ensure normalization/trimming applied
	for _, c := range res.Contacts {
		if strings.Contains(c.Phone, " ") {
			t.Fatalf("phone contains spaces after normalization: %q", c.Phone)
		}
		if c.AccountIndex < 1 || c.AccountIndex > 6 {
			t.Fatalf("invalid account index: %d", c.AccountIndex)
		}
	}
}

func TestLoaderDedupPrefersLaterPath(t *testing.T) {
	dir := t.TempDir()

	aPath := filepath.Join(dir, "a.yaml")
	bPath := filepath.Join(dir, "z.yaml")

	tpl := `- first_name: Jane
  last_name: Roe
  phone: "8000"
  account_index: 1
`
	if err := os.WriteFile(aPath, []byte(tpl), 0o644); err != nil {
		t.Fatal(err)
	}

	override := `- first_name: Jane
  last_name: Roe
  phone: "8000"
  account_index: 1
`
	if err := os.WriteFile(bPath, []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := load.New(dir, testutil.NewTestLogger())
	res, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(res.Contacts) != 1 {
		t.Fatalf("expected 1 contact, got %d", len(res.Contacts))
	}

	c := res.Contacts[0]
	if c.SourcePath != bPath {
		t.Fatalf("expected contact from lexicographically later path %s, got %s", bPath, c.SourcePath)
	}
}

func TestLoaderSkipsInvalidContacts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "oops.yaml")
	data := `- first_name: Bad
  last_name: Data
  phone: "1111"
  account_index: 10
- first_name: Good
  last_name: Data
  phone: "2222"
  account_index: 1
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	loader := load.New(dir, testutil.NewTestLogger())
	res, err := loader.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(res.Contacts) != 1 {
		t.Fatalf("expected 1 valid contact, got %d", len(res.Contacts))
	}
	if res.Contacts[0].FirstName != "Good" {
		t.Fatalf("expected Good contact, got %s", res.Contacts[0].FirstName)
	}
}
