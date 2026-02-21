package calls

import (
	"testing"
	"time"
)

type testLogger struct{}

func (testLogger) Info(string, ...any)  {}
func (testLogger) Warn(string, ...any)  {}
func (testLogger) Debug(string, ...any) {}

func TestHandleAMIEventLifecycle(t *testing.T) {
	svc := NewService(Options{MaxHistory: 100, Retention: 7 * 24 * time.Hour}, testLogger{})

	svc.HandleAMIEvent(map[string]string{
		"Event":       "Newchannel",
		"Linkedid":    "abc",
		"Uniqueid":    "u1",
		"CallerIDNum": "2601",
		"Exten":       "2602",
	})
	svc.HandleAMIEvent(map[string]string{
		"Event":    "BridgeEnter",
		"Linkedid": "abc",
		"Uniqueid": "u1",
	})
	svc.HandleAMIEvent(map[string]string{
		"Event":     "Hangup",
		"Linkedid":  "abc",
		"Uniqueid":  "u1",
		"Cause-txt": "Normal Clearing",
	})

	snap := svc.Snapshot()
	if len(snap.Active) != 0 {
		t.Fatalf("expected no active calls, got %d", len(snap.Active))
	}
	if len(snap.History) != 1 {
		t.Fatalf("expected 1 history record, got %d", len(snap.History))
	}
	got := snap.History[0]
	if got.From != "2601" || got.To != "2602" {
		t.Fatalf("unexpected history parties: %+v", got)
	}
	if got.EndReason != "Normal Clearing" {
		t.Fatalf("expected end reason to be captured, got %q", got.EndReason)
	}
}

func TestParseDialString(t *testing.T) {
	if got := parseDialString("PJSIP/8081,30"); got != "8081" {
		t.Fatalf("expected 8081, got %q", got)
	}
	if got := parseDialString("PJSIP/2601&PJSIP/2602,20"); got != "2601" {
		t.Fatalf("expected first dial target, got %q", got)
	}
}

func TestHandleAMIEventDialSubEventBeginCreatesActive(t *testing.T) {
	svc := NewService(Options{MaxHistory: 100, Retention: 7 * 24 * time.Hour}, testLogger{})

	svc.HandleAMIEvent(map[string]string{
		"Event":        "Dial",
		"SubEvent":     "Begin",
		"LinkedID":     "linked-1",
		"SrcUniqueId":  "src-1",
		"DestUniqueId": "dst-1",
		"CallerIDNum":  "2601",
		"DialString":   "PJSIP/8081,30",
	})

	snap := svc.Snapshot()
	if len(snap.Active) != 1 {
		t.Fatalf("expected 1 active call, got %d", len(snap.Active))
	}
	got := snap.Active[0]
	if got.From != "2601" || got.To != "8081" {
		t.Fatalf("unexpected call parties: %+v", got)
	}
}
