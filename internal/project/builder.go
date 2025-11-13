package project

import (
	"time"

	"github.com/n3wscott/phonebook/internal/asterisk"
	"github.com/n3wscott/phonebook/internal/config"
	"github.com/n3wscott/phonebook/internal/load"
	"github.com/n3wscott/phonebook/internal/model"
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

	last := latest(metas)

	return State{
		Config:     cfg,
		Defaults:   defs,
		Contacts:   contactRes.Contacts,
		Phonebook:  xmlBytes,
		PJSIP:      pjsipBytes,
		Extensions: extensionsBytes,
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
