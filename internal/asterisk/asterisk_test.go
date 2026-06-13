package asterisk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n3wscott/phonebook/internal/config"
	"github.com/n3wscott/phonebook/internal/model"
)

func TestRenderPJSIPMatchesGolden(t *testing.T) {
	cfg := sampleConfig()
	contacts := sampleContacts()
	got, err := RenderPJSIP(cfg, contacts)
	if err != nil {
		t.Fatalf("RenderPJSIP() error = %v", err)
	}
	want := readGolden(t, "testdata/asterisk/pjsip.conf")
	if string(got) != string(want) {
		t.Fatalf("pjsip.conf mismatch\nGot:\n%s\nWant:\n%s", got, want)
	}
}

func TestRenderExtensionsMatchesGolden(t *testing.T) {
	cfg := sampleConfig()
	contacts := sampleContacts()
	got, err := RenderExtensions(cfg, contacts)
	if err != nil {
		t.Fatalf("RenderExtensions() error = %v", err)
	}
	want := readGolden(t, "testdata/asterisk/extensions.conf")
	if string(got) != string(want) {
		t.Fatalf("extensions.conf mismatch\nGot:\n%s\nWant:\n%s", got, want)
	}
}

func TestRenderExtensionsWithConferencesAndMessages(t *testing.T) {
	cfg := sampleConfig()
	cfg.Dialplan.Includes = []string{"legacy"}
	cfg.Dialplan.Conferences = []config.Conference{{Extension: "2600", Room: "2600", Context: "conferences"}}
	cfg.Dialplan.Messages = config.Messages{Enabled: true, Context: "messages", Pattern: "_X."}

	got, err := RenderExtensions(cfg, sampleContacts())
	if err != nil {
		t.Fatalf("RenderExtensions() error = %v", err)
	}

	want := `[internal]
include => legacy
include => conferences
include => messages
exten => 101,1,Dial(PJSIP/101)
exten => 102,1,Dial(PJSIP/102)

[conferences]
exten => 2600,1,Answer()
 same => n,ConfBridge(2600)
 same => n,Hangup()

[messages]
exten => _X.,1,NoOp(Incoming SIP MESSAGE)
 same => n,MessageSend(pjsip:${EXTEN},${MESSAGE(from)})
 same => n,Hangup()

`
	if string(got) != want {
		t.Fatalf("extensions.conf mismatch\nGot:\n%s\nWant:\n%s", got, want)
	}
}

func TestRenderExtensionsWithApplication(t *testing.T) {
	cfg := sampleConfig()
	cfg.Dialplan.Applications = []config.Application{{
		Extension: "5653",
		Steps: []string{
			"Answer()",
			"AGI(/usr/local/libexec/madrona-pbx/jokes,agi)",
			"Hangup()",
		},
	}}
	contacts := append(sampleContacts(), model.Contact{
		ID:            "joke",
		FirstName:     "Joke",
		Extension:     "5653",
		PhonebookOnly: true,
		Phones:        []model.Phone{{Number: "5653", AccountIndex: 1}},
	})

	got, err := RenderExtensions(cfg, contacts)
	if err != nil {
		t.Fatalf("RenderExtensions() error = %v", err)
	}

	want := `[internal]
exten => 101,1,Dial(PJSIP/101)
exten => 102,1,Dial(PJSIP/102)
exten => 5653,1,Answer()
 same => n,AGI(/usr/local/libexec/madrona-pbx/jokes,agi)
 same => n,Hangup()

`
	if string(got) != want {
		t.Fatalf("extensions.conf mismatch\nGot:\n%s\nWant:\n%s", got, want)
	}
}

func TestPhonebookOnlyContactDoesNotRenderPJSIPOrDialplan(t *testing.T) {
	cfg := sampleConfig()
	cfg.Dialplan.Conferences = []config.Conference{{Extension: "2600", Room: "2600", Context: "conferences"}}
	contacts := append(sampleContacts(), model.Contact{
		ID:            "hangout",
		FirstName:     "Hangout",
		Extension:     "2600",
		PhonebookOnly: true,
		Phones:        []model.Phone{{Number: "2600", AccountIndex: 1}},
	})

	pjsip, err := RenderPJSIP(cfg, contacts)
	if err != nil {
		t.Fatalf("RenderPJSIP() error = %v", err)
	}
	if got := string(pjsip); contains(got, "[2600]") || contains(got, "Auth & AOR for extension 2600") {
		t.Fatalf("phonebook-only contact should not render PJSIP sections:\n%s", got)
	}

	extensions, err := RenderExtensions(cfg, contacts)
	if err != nil {
		t.Fatalf("RenderExtensions() error = %v", err)
	}
	got := string(extensions)
	if contains(got, "Dial(PJSIP/2600)") {
		t.Fatalf("phonebook-only contact should not render direct dialplan:\n%s", got)
	}
	if !contains(got, "ConfBridge(2600)") {
		t.Fatalf("conference extension should still render:\n%s", got)
	}
}

func TestHiddenContactStillRendersPJSIPAndDialplan(t *testing.T) {
	cfg := sampleConfig()
	contacts := append(sampleContacts(), model.Contact{
		ID:        "hidden",
		FirstName: "Hidden",
		LastName:  "Service",
		Extension: "5653",
		Hidden:    true,
		Auth: model.ContactAuth{
			Username: "5653",
			Password: "pw5653",
		},
		AOR: model.ContactAOR{
			MaxContacts:      1,
			RemoveExisting:   true,
			QualifyFrequency: 30,
		},
		Endpoint: model.ContactEndpoint{Template: "endpoint-template"},
	})

	pjsip, err := RenderPJSIP(cfg, contacts)
	if err != nil {
		t.Fatalf("RenderPJSIP() error = %v", err)
	}
	if got := string(pjsip); !contains(got, "[5653]") || !contains(got, "password=pw5653") {
		t.Fatalf("hidden contact should render PJSIP sections:\n%s", got)
	}

	extensions, err := RenderExtensions(cfg, contacts)
	if err != nil {
		t.Fatalf("RenderExtensions() error = %v", err)
	}
	if got := string(extensions); !contains(got, "exten => 5653,1,Dial(PJSIP/5653)") {
		t.Fatalf("hidden contact should render direct dialplan:\n%s", got)
	}
}

func sampleConfig() config.Config {
	return config.Config{
		Global: map[string]any{"user_agent": "Asterisk"},
		Network: config.Network{
			ExternalSignalingAddress: "198.51.100.1",
			ExternalMediaAddress:     "198.51.100.1",
			LocalNet:                 []string{"192.168.1.0/24"},
		},
		Transports: []config.Transport{
			{
				Name:     "transport-udp",
				Protocol: "udp",
				Bind:     "0.0.0.0:5060",
				Extra:    map[string]any{"tos": 184},
			},
		},
		EndpointTemplates: []config.EndpointConfig{
			{
				Name:  "endpoint-template",
				Extra: map[string]any{"context": "internal", "allow": []string{"ulaw"}},
			},
		},
		Dialplan: config.Dialplan{Context: "internal"},
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func sampleContacts() []model.Contact {
	return []model.Contact{
		{
			ID:        "alpha",
			FirstName: "Alpha",
			LastName:  "User",
			Extension: "101",
			Auth: model.ContactAuth{
				Username: "101",
				Password: "pw101",
			},
			AOR: model.ContactAOR{
				MaxContacts:      1,
				RemoveExisting:   true,
				QualifyFrequency: 30,
			},
			Endpoint: model.ContactEndpoint{Template: "endpoint-template"},
		},
		{
			ID:        "beta",
			FirstName: "Beta",
			LastName:  "User",
			Extension: "102",
			Auth: model.ContactAuth{
				Username: "user102",
				Password: "pw102",
			},
			AOR: model.ContactAOR{
				MaxContacts:      2,
				RemoveExisting:   false,
				QualifyFrequency: 60,
			},
			Endpoint: model.ContactEndpoint{Template: "endpoint-template"},
		},
	}
}

func readGolden(t *testing.T, rel string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", rel)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", path, err)
	}
	return data
}
