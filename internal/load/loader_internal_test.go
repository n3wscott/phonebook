package load

import (
	"testing"
	"time"

	"github.com/n3wscott/phonebook/internal/model"
)

func TestShouldReplace(t *testing.T) {
	now := time.Now()
	earlier := now.Add(-time.Minute)

	base := model.Contact{SourcePath: "/a.yaml", SourceMod: earlier}
	laterPath := model.Contact{SourcePath: "/b.yaml", SourceMod: earlier}

	if !shouldReplace(base, laterPath) {
		t.Fatalf("expected later path to win")
	}
	if shouldReplace(laterPath, base) {
		t.Fatalf("earlier path should not win")
	}

	samePathNewer := model.Contact{SourcePath: base.SourcePath, SourceMod: now}
	if !shouldReplace(base, samePathNewer) {
		t.Fatalf("expected newer mod time to win when paths equal")
	}
}

func TestNormalizePhone(t *testing.T) {
	got, err := normalizePhone(" +1 555 1234 ,#")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "+15551234,#" {
		t.Fatalf("unexpected normalization result: %q", got)
	}

	if _, err := normalizePhone("1234abc"); err == nil {
		t.Fatalf("expected error for invalid characters")
	}
}
