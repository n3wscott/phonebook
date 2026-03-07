package project

import (
	"fmt"
	"time"

	"github.com/n3wscott/phonebook/internal/asterisk"
	"github.com/n3wscott/phonebook/internal/config"
	"github.com/n3wscott/phonebook/internal/load"
	"github.com/n3wscott/phonebook/internal/model"
	"github.com/n3wscott/phonebook/internal/provision"
	"github.com/n3wscott/phonebook/internal/xmlgen"
)

// Logger mirrors the loader/logger expectations.
type Logger interface {
	Warn(msg string, args ...any)
	Info(msg string, args ...any)
}

// Builder compiles configuration + contacts into renderable assets.
type Builder struct {
	Dir    string
	Logger Logger
}

// State is the compiled view of the repository.
type State struct {
	Config     config.Config
	Defaults   config.Defaults
	Contacts   []model.Contact
	Phonebook  []byte
	PJSIP      []byte
	Extensions []byte
	Provision  map[string][]byte
	Files      []config.FileMeta
	LastUpdate time.Time
}

// Build loads the repo and renders XML + Asterisk configs.
func (b *Builder) Build() (State, error) {
	cfg, defs, metas, err := config.Load(b.Dir)
	if err != nil {
		return State{}, err
	}

	loader := load.New(b.Dir, b.Logger)
	contactRes, err := loader.LoadContacts(cfg, defs)
	if err != nil {
		return State{}, err
	}
	metas = append(metas, contactRes.Files...)

	xmlBytes, err := xmlgen.Build(contactRes.Contacts)
	if err != nil {
		return State{}, err
	}
	pjsipBytes, err := asterisk.RenderPJSIP(cfg, contactRes.Contacts)
	if err != nil {
		return State{}, err
	}
	extensionsBytes, err := asterisk.RenderExtensions(cfg, contactRes.Contacts)
	if err != nil {
		return State{}, err
	}

	provHost := globalString(cfg.Global, "provision_host", "cash-pbx.lan")
	provPort := globalString(cfg.Global, "provision_port", defaultPortFromAddr(cfg.Server.Addr))
	sipServer := globalString(cfg.Global, "provision_sip_server", provHost)
	sipPort := globalString(cfg.Global, "provision_sip_port", "5060")
	provisionHostPort := globalString(cfg.Global, "provision_hostport", hostPort(provHost, provPort))
	provisionURL := globalString(cfg.Global, "provision_url", fmt.Sprintf("http://%s/prov/", provisionHostPort))
	phonebookURL := globalString(cfg.Global, "provision_phonebook_url", fmt.Sprintf("http://%s%sphonebook.xml", provisionHostPort, cfg.Server.BasePath))
	outboundProxy := globalString(cfg.Global, "provision_outbound_proxy", "")

	provFiles, provMetas, err := provision.Build(b.Dir, provision.Options{
		SIPServer:         sipServer,
		SIPPort:           sipPort,
		OutboundProxy:     outboundProxy,
		PhonebookURL:      phonebookURL,
		ProvisionURL:      provisionURL,
		ProvisionHostport: provisionHostPort,
	})
	if err != nil {
		return State{}, err
	}
	metas = append(metas, provMetas...)

	last := latest(metas)

	return State{
		Config:     cfg,
		Defaults:   defs,
		Contacts:   contactRes.Contacts,
		Phonebook:  xmlBytes,
		PJSIP:      pjsipBytes,
		Extensions: extensionsBytes,
		Provision:  provFiles,
		Files:      metas,
		LastUpdate: last,
	}, nil
}

func latest(files []config.FileMeta) time.Time {
	var t time.Time
	for _, f := range files {
		if f.ModTime.After(t) {
			t = f.ModTime
		}
	}
	return t
}

func globalString(m map[string]any, key, fallback string) string {
	if m == nil {
		return fallback
	}
	raw, ok := m[key]
	if !ok || raw == nil {
		return fallback
	}
	switch v := raw.(type) {
	case string:
		if v == "" {
			return fallback
		}
		return v
	case int:
		return fmt.Sprintf("%d", v)
	case int64:
		return fmt.Sprintf("%d", v)
	case float64:
		return fmt.Sprintf("%.0f", v)
	default:
		return fallback
	}
}

func defaultPortFromAddr(addr string) string {
	if addr == "" {
		return "8080"
	}
	if addr[0] == ':' {
		return addr[1:]
	}
	idx := -1
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			idx = i
			break
		}
	}
	if idx == -1 || idx+1 >= len(addr) {
		return "8080"
	}
	return addr[idx+1:]
}

func hostPort(host, port string) string {
	if host == "" {
		return ""
	}
	if port == "" || port == "80" || port == "443" {
		return host
	}
	return host + ":" + port
}
