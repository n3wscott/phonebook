package load

import "testing"

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
