package load

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/n3wscott/phonebook/internal/config"
	"github.com/n3wscott/phonebook/internal/model"
	"gopkg.in/yaml.v3"
)

// Logger is the subset of slog we need.
type Logger interface {
	Warn(msg string, args ...any)
}

// Loader normalizes contacts from contacts/.
type Loader struct {
	dir    string
	logger Logger
}

// New returns a Loader.
func New(dir string, logger Logger) *Loader {
	return &Loader{dir: dir, logger: logger}
}

// Result is the normalized contact list plus metadata.
type Result struct {
	Contacts []model.Contact
	Files    []config.FileMeta
}

// LoadContacts scans contacts/ and returns normalized contacts.
func (l *Loader) LoadContacts(cfg config.Config, defs config.Defaults) (Result, error) {
	dir := filepath.Join(l.dir, "contacts")
	files, err := collectYAML(dir)
	if err != nil {
		return Result{}, err
	}

	templateSet := make(map[string]struct{}, len(cfg.EndpointTemplates))
	for _, t := range cfg.EndpointTemplates {
		templateSet[t.Name] = struct{}{}
	}

	dedup := map[string]model.Contact{}
	metas := make([]config.FileMeta, 0, len(files))

	for _, fd := range files {
		contacts, err := l.parseFile(fd, defs, templateSet)
		if err != nil {
			return Result{}, err
		}
		metas = append(metas, config.FileMeta{Path: fd.Path, ModTime: fd.ModTime})
		for _, c := range contacts {
			if existing, ok := dedup[c.Extension]; ok {
				l.logger.Warn("duplicate extension detected, overriding", "ext", c.Extension, "prev", existing.SourcePath, "next", c.SourcePath)
			}
			dedup[c.Extension] = c
		}
	}

	contacts := make([]model.Contact, 0, len(dedup))
	for _, c := range dedup {
		contacts = append(contacts, c)
	}
	sort.Slice(contacts, func(i, j int) bool {
		if contacts[i].Extension == contacts[j].Extension {
			return contacts[i].SourcePath < contacts[j].SourcePath
		}
		return contacts[i].Extension < contacts[j].Extension
	})

	return Result{Contacts: contacts, Files: metas}, nil
}

type fileDescriptor struct {
	Path    string
	ModTime time.Time
}

func collectYAML(root string) ([]fileDescriptor, error) {
	var files []fileDescriptor
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !isYAML(path) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files = append(files, fileDescriptor{Path: path, ModTime: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].Path == files[j].Path {
			return files[i].ModTime.Before(files[j].ModTime)
		}
		return files[i].Path < files[j].Path
	})
	return files, nil
}

func isYAML(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}

func (l *Loader) parseFile(fd fileDescriptor, defs config.Defaults, templates map[string]struct{}) ([]model.Contact, error) {
	data, err := os.ReadFile(fd.Path)
	if err != nil {
		return nil, fmt.Errorf("read contacts %s: %w", fd.Path, err)
	}
	rawContacts, err := parseContacts(data)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", fd.Path, err)
	}

	out := make([]model.Contact, 0, len(rawContacts))
	for _, rc := range rawContacts {
		contact, err := rc.Normalize(fd, defs, templates)
		if err != nil {
			l.logger.Warn("skipping contact", "path", fd.Path, "err", err)
			continue
		}
		out = append(out, contact)
	}
	return out, nil
}

func parseContacts(data []byte) ([]rawContact, error) {
	var withKey struct {
		Contacts []rawContact `yaml:"contacts"`
	}
	if err := yaml.Unmarshal(data, &withKey); err == nil && len(withKey.Contacts) > 0 {
		return withKey.Contacts, nil
	}

	var list []rawContact
	if err := yaml.Unmarshal(data, &list); err == nil && len(list) > 0 {
		return list, nil
	} else if err != nil {
		return nil, err
	}

	// empty file is fine
	return nil, nil
}

type rawContact struct {
	ID           string      `yaml:"id"`
	FirstName    string      `yaml:"first_name"`
	LastName     string      `yaml:"last_name"`
	Ext          string      `yaml:"ext"`
	Password     string      `yaml:"password"`
	AccountIndex *int        `yaml:"account_index"`
	GroupID      *int        `yaml:"group_id"`
	Nickname     string      `yaml:"nickname"`
	Phones       []rawPhone  `yaml:"phones"`
	Auth         rawAuth     `yaml:"auth"`
	AOR          rawAOR      `yaml:"aor"`
	Endpoint     rawEndpoint `yaml:"endpoint"`
}

type rawPhone struct {
	Number       string `yaml:"number"`
	AccountIndex *int   `yaml:"account_index"`
}

type rawAuth struct {
	Username *string `yaml:"username"`
}

type rawAOR struct {
	MaxContacts      *int  `yaml:"max_contacts"`
	RemoveExisting   *bool `yaml:"remove_existing"`
	QualifyFrequency *int  `yaml:"qualify_frequency"`
}

type rawEndpoint struct {
	Template string `yaml:"template"`
}

func (rc rawContact) Normalize(fd fileDescriptor, defs config.Defaults, templates map[string]struct{}) (model.Contact, error) {
	ext := strings.TrimSpace(rc.Ext)
	if ext == "" {
		return model.Contact{}, errors.New("contact missing ext")
	}
	password := strings.TrimSpace(rc.Password)
	if password == "" {
		return model.Contact{}, fmt.Errorf("contact %s missing password", ext)
	}
	first := strings.TrimSpace(rc.FirstName)
	last := strings.TrimSpace(rc.LastName)
	if first == "" && last == "" {
		return model.Contact{}, fmt.Errorf("contact %s missing both first_name and last_name", ext)
	}

	group := normalizeGroup(rc.GroupID)
	if group != nil && (*group < 0 || *group > 9) {
		return model.Contact{}, fmt.Errorf("contact %s group_id out of range", ext)
	}

	var fallbackIdx int = 1
	if rc.AccountIndex != nil {
		fallbackIdx = *rc.AccountIndex
	}
	if fallbackIdx < 1 || fallbackIdx > 6 {
		return model.Contact{}, fmt.Errorf("contact %s account_index out of range", ext)
	}

	phones, err := rc.buildPhones(fallbackIdx, ext)
	if err != nil {
		return model.Contact{}, err
	}

	username := ext
	if rc.Auth.Username != nil {
		username = strings.TrimSpace(*rc.Auth.Username)
	} else if !defs.Auth.UsernameEqualsExt {
		return model.Contact{}, fmt.Errorf("contact %s missing auth.username and defaults.username_equals_ext=false", ext)
	}
	if username == "" {
		return model.Contact{}, fmt.Errorf("contact %s has empty auth username", ext)
	}

	aor := model.ContactAOR{
		MaxContacts:      defs.AOR.MaxContacts,
		RemoveExisting:   defs.AOR.RemoveExisting,
		QualifyFrequency: defs.AOR.QualifyFrequency,
	}
	if rc.AOR.MaxContacts != nil {
		aor.MaxContacts = *rc.AOR.MaxContacts
	}
	if rc.AOR.RemoveExisting != nil {
		aor.RemoveExisting = *rc.AOR.RemoveExisting
	}
	if rc.AOR.QualifyFrequency != nil {
		aor.QualifyFrequency = *rc.AOR.QualifyFrequency
	}

	template := strings.TrimSpace(rc.Endpoint.Template)
	if template == "" {
		template = defs.Endpoint.Template
	}
	if _, ok := templates[template]; !ok {
		return model.Contact{}, fmt.Errorf("contact %s references unknown endpoint template %q", ext, template)
	}

	return model.Contact{
		ID:           strings.TrimSpace(rc.ID),
		FirstName:    first,
		LastName:     last,
		Extension:    ext,
		Password:     password,
		GroupID:      group,
		AccountIndex: rc.AccountIndex,
		Phones:       phones,
		Nickname:     strings.TrimSpace(rc.Nickname),
		Auth: model.ContactAuth{
			Username: username,
			Password: password,
		},
		AOR:        aor,
		Endpoint:   model.ContactEndpoint{Template: template},
		SourcePath: fd.Path,
		SourceMod:  fd.ModTime,
	}, nil
}

func (rc rawContact) buildPhones(fallbackIdx int, ext string) ([]model.Phone, error) {
	if len(rc.Phones) == 0 {
		number, err := normalizePhone(ext)
		if err != nil {
			return nil, fmt.Errorf("contact %s invalid extension for phonebook: %w", ext, err)
		}
		return []model.Phone{{Number: number, AccountIndex: fallbackIdx}}, nil
	}

	phones := make([]model.Phone, 0, len(rc.Phones))
	for _, p := range rc.Phones {
		number := strings.TrimSpace(p.Number)
		if number == "" {
			return nil, fmt.Errorf("contact %s has empty phone number entry", ext)
		}
		normalized, err := normalizePhone(number)
		if err != nil {
			return nil, fmt.Errorf("contact %s phone invalid: %w", ext, err)
		}
		idx := fallbackIdx
		if p.AccountIndex != nil {
			idx = *p.AccountIndex
		}
		if idx < 1 || idx > 6 {
			return nil, fmt.Errorf("contact %s phone account_index out of range", ext)
		}
		phones = append(phones, model.Phone{Number: normalized, AccountIndex: idx})
	}
	return phones, nil
}

func normalizeGroup(g *int) *int {
	if g == nil {
		return nil
	}
	val := *g
	return &val
}

func normalizePhone(input string) (string, error) {
	var b strings.Builder
	for _, r := range input {
		if unicode.IsSpace(r) {
			continue
		}
		if (r >= '0' && r <= '9') || r == '+' || r == '*' || r == '#' || r == ',' {
			b.WriteRune(r)
			continue
		}
		return "", fmt.Errorf("invalid character %q", r)
	}
	number := b.String()
	if number == "" {
		return "", errors.New("phone empty after normalization")
	}
	return number, nil
}
