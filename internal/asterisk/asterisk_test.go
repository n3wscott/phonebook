package asterisk

import (
	"os"
	"path/filepath"
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
