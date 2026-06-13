package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/n3wscott/phonebook/internal/calls"
	"github.com/n3wscott/phonebook/internal/model"
	"github.com/n3wscott/phonebook/internal/testutil"
)

type recordingSender struct {
	messages []calls.Message
	err      error
}

func (s *recordingSender) SendMessage(_ context.Context, msg calls.Message) error {
	s.messages = append(s.messages, msg)
	return s.err
}

func TestBroadcastContactsFiltersNonSIPContacts(t *testing.T) {
	logger := testutil.NewTestLogger()
	srv := NewServer(Config{
		Addr:      ":0",
		BasePath:  "/xml/",
		Broadcast: BroadcastConfig{Enabled: true},
	}, logger)
	srv.Update([]model.Contact{
		{FirstName: "Alpha", Extension: "1001"},
		{FirstName: "Hidden", Extension: "1002", Hidden: true},
		{FirstName: "Room", Extension: "2600", PhonebookOnly: true},
	}, []byte("<AddressBook></AddressBook>"), time.Unix(0, 0))

	contacts := srv.buildBroadcastContacts()
	if len(contacts) != 1 {
		t.Fatalf("expected 1 contact, got %+v", contacts)
	}
	if contacts[0].ID != "1001" {
		t.Fatalf("expected 1001, got %+v", contacts[0])
	}
}

func TestBroadcastSendValidatesAndSendsAllowedRecipients(t *testing.T) {
	logger := testutil.NewTestLogger()
	sender := &recordingSender{}
	srv := NewServer(Config{
		Addr:     ":0",
		BasePath: "/xml/",
		Broadcast: BroadcastConfig{
			Enabled:  true,
			From:     "Operator <sip:0@example.test>",
			MaxChars: 20,
			Sender:   sender,
		},
	}, logger)
	srv.Update([]model.Contact{
		{FirstName: "Alpha", Extension: "1001"},
		{FirstName: "Hidden", Extension: "1002", Hidden: true},
	}, []byte("<AddressBook></AddressBook>"), time.Unix(0, 0))

	body := bytes.NewBufferString(`{"recipients":["1001","1001","1002","9999"],"message":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/broadcast/send", body)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(sender.messages) != 1 {
		t.Fatalf("expected 1 sent message, got %+v", sender.messages)
	}
	msg := sender.messages[0]
	if msg.Destination != "pjsip:1001" || msg.To != "sip:1001" || msg.From != "Operator <sip:0@example.test>" || msg.Body != "hello" {
		t.Fatalf("unexpected message: %+v", msg)
	}

	var resp broadcastSendResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Attempted != 1 || len(resp.Sent) != 1 || resp.Sent[0] != "1001" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestBroadcastSendRejectsOverLimitMessage(t *testing.T) {
	logger := testutil.NewTestLogger()
	srv := NewServer(Config{
		Addr:      ":0",
		BasePath:  "/xml/",
		Broadcast: BroadcastConfig{Enabled: true, MaxChars: 3, Sender: &recordingSender{}},
	}, logger)
	srv.Update([]model.Contact{{FirstName: "Alpha", Extension: "1001"}}, []byte("<AddressBook></AddressBook>"), time.Unix(0, 0))

	req := httptest.NewRequest(http.MethodPost, "/api/broadcast/send", strings.NewReader(`{"recipients":["1001"],"message":"hello"}`))
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}
