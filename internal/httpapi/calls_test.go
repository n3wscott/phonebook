package httpapi

import (
	"testing"

	"github.com/n3wscott/phonebook/internal/model"
)

func TestCanonicalParty(t *testing.T) {
	if got := canonicalParty("Costin - 2601"); got != "2601" {
		t.Fatalf("expected canonical number 2601, got %q", got)
	}
	if got := canonicalParty("unknown"); got != "unknown" {
		t.Fatalf("expected raw fallback for non-numeric value, got %q", got)
	}
}

func TestBuildNameLookupUsesFirstAndLastName(t *testing.T) {
	contacts := []model.Contact{
		{
			FirstName: "Scott",
			LastName:  "Nichols",
			Extension: "2601",
			Phones: []model.Phone{
				{Number: "8081", AccountIndex: 1},
			},
		},
		{
			Extension: "9999",
		},
	}
	lookup := buildNameLookup(contacts)
	if got := resolveName(lookup, "2601"); got != "Scott Nichols" {
		t.Fatalf("expected name for extension 2601, got %q", got)
	}
	if got := resolveName(lookup, "8081"); got != "Scott Nichols" {
		t.Fatalf("expected name for phone 8081, got %q", got)
	}
	if got := resolveName(lookup, "9999"); got != "" {
		t.Fatalf("expected empty name for contact with no first/last, got %q", got)
	}
}

func TestDashboardContactStateOnlyShowsInUseForActiveCalls(t *testing.T) {
	tests := []struct {
		name   string
		state  string
		active bool
		want   string
	}{
		{
			name:   "raw in-use presence without active call is connected",
			state:  "in-use",
			active: false,
			want:   "connected",
		},
		{
			name:   "raw ringing presence without active call is connected",
			state:  "ringing",
			active: false,
			want:   "connected",
		},
		{
			name:   "active call is in-call",
			state:  "connected",
			active: true,
			want:   "in-call",
		},
		{
			name:   "offline remains disconnected",
			state:  "offline",
			active: false,
			want:   "disconnected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dashboardContactState(tt.state, tt.active); got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}
