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

	"github.com/n3wscott/phonebook/internal/model"
	"gopkg.in/yaml.v3"
)

// Loader scans a directory tree, parses YAML contacts, and emits normalized contacts.
type Loader struct {
	dir    string
	logger Logger
}

// Logger represents the minimal logging interface needed by the loader.
type Logger interface {
	Warn(msg string, args ...any)
	Info(msg string, args ...any)
	Debug(msg string, args ...any)
}

// Result contains the normalized contacts and file metadata.
type Result struct {
	Contacts []model.Contact
	Files    []FileMeta
}

// FileMeta captures metadata about a contributing file.
type FileMeta struct {
	Path    string
	ModTime time.Time
}

// LastModified returns the most recent mod time among contributing files.
func (r Result) LastModified() time.Time {
	var latest time.Time
	for _, f := range r.Files {
		if f.ModTime.After(latest) {
			latest = f.ModTime
		}
	}
	return latest
}

// New creates a new Loader for the provided directory.
func New(dir string, logger Logger) *Loader {
	return &Loader{dir: dir, logger: logger}
}

// Load walks the directory tree and builds a list of contacts.
func (l *Loader) Load() (Result, error) {
	files, err := l.discover()
	if err != nil {
		return Result{}, err
	}

	dedup := make(map[string]model.Contact)
	metas := make([]FileMeta, 0, len(files))

	for _, file := range files {
		contacts, err := l.parseFile(file)
		if err != nil {
			return Result{}, err
		}
		metas = append(metas, FileMeta{Path: file.Path, ModTime: file.ModTime})
		for _, c := range contacts {
			key := contactKey(c)
			if existing, ok := dedup[key]; ok {
				if shouldReplace(existing, c) {
					dedup[key] = c
				}
				continue
			}
			dedup[key] = c
		}
	}

	contacts := make([]model.Contact, 0, len(dedup))
	for _, c := range dedup {
		contacts = append(contacts, c)
	}

	sort.Slice(contacts, func(i, j int) bool {
		if contacts[i].LastName != contacts[j].LastName {
			return contacts[i].LastName < contacts[j].LastName
		}
		if contacts[i].FirstName != contacts[j].FirstName {
			return contacts[i].FirstName < contacts[j].FirstName
		}
		if contacts[i].Phone != contacts[j].Phone {
			return contacts[i].Phone < contacts[j].Phone
		}
		return contacts[i].AccountIndex < contacts[j].AccountIndex
	})

	return Result{Contacts: contacts, Files: metas}, nil
}

// fileDescriptor is an intermediate struct for discovery.
type fileDescriptor struct {
	Path    string
	ModTime time.Time
}

func (l *Loader) discover() ([]fileDescriptor, error) {
	var files []fileDescriptor
	err := filepath.WalkDir(l.dir, func(path string, d fs.DirEntry, err error) error {
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
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func (l *Loader) parseFile(fd fileDescriptor) ([]model.Contact, error) {
	data, err := os.ReadFile(fd.Path)
	if err != nil {
		return nil, err
	}
	rawContacts, err := parseYAMLContacts(data)
	if err != nil {
		l.logger.Warn("failed to parse YAML", "path", fd.Path, "err", err)
		return nil, err
	}

	contacts := make([]model.Contact, 0, len(rawContacts))
	for _, rc := range rawContacts {
		contact, err := rc.Normalize(fd)
		if err != nil {
			l.logger.Warn("invalid contact", "path", fd.Path, "err", err)
			continue
		}
		contacts = append(contacts, contact)
	}
	return contacts, nil
}

func isYAML(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}

func contactKey(c model.Contact) string {
	return strings.ToLower(strings.Join([]string{
		strings.TrimSpace(c.LastName),
		strings.TrimSpace(c.FirstName),
		strings.TrimSpace(c.Phone),
		fmt.Sprintf("%d", c.AccountIndex),
	}, "|"))
}

func shouldReplace(existing, cand model.Contact) bool {
	if cand.SourcePath > existing.SourcePath {
		return true
	}
	if cand.SourcePath < existing.SourcePath {
		return false
	}
	return cand.SourceMod.After(existing.SourceMod)
}

// rawContact mirrors the YAML schema.
type rawContact struct {
	FirstName    string `yaml:"first_name"`
	LastName     string `yaml:"last_name"`
	Phone        string `yaml:"phone"`
	AccountIndex *int   `yaml:"account_index"`
	GroupID      *int   `yaml:"group_id"`
	Nickname     string `yaml:"nickname"`
}

func parseYAMLContacts(data []byte) ([]rawContact, error) {
	var firstErr error

	var list []rawContact
	if err := yaml.Unmarshal(data, &list); err == nil {
		if len(list) > 0 {
			return list, nil
		}
	} else {
		firstErr = err
	}

	var wrapped struct {
		Contacts []rawContact `yaml:"contacts"`
	}
	if err := yaml.Unmarshal(data, &wrapped); err == nil {
		if len(wrapped.Contacts) > 0 {
			return wrapped.Contacts, nil
		}
	} else if firstErr == nil {
		firstErr = err
	}

	if len(list) > 0 {
		return list, nil
	}

	if firstErr != nil {
		return nil, firstErr
	}

	// At this point treat as an empty file.
	return nil, nil
}

func (rc rawContact) Normalize(fd fileDescriptor) (model.Contact, error) {
	first := strings.TrimSpace(rc.FirstName)
	last := strings.TrimSpace(rc.LastName)
	if first == "" && last == "" {
		return model.Contact{}, errors.New("contact missing both first_name and last_name")
	}

	phone := strings.TrimSpace(rc.Phone)
	if phone == "" {
		return model.Contact{}, errors.New("contact missing phone")
	}
	normalized, err := normalizePhone(phone)
	if err != nil {
		return model.Contact{}, err
	}

	if rc.AccountIndex == nil {
		return model.Contact{}, errors.New("account_index is required")
	}
	idx := *rc.AccountIndex
	if idx < 1 || idx > 6 {
		return model.Contact{}, fmt.Errorf("account_index %d out of range", idx)
	}

	var groupPtr *int
	if rc.GroupID != nil {
		gid := *rc.GroupID
		if gid < 0 || gid > 9 {
			return model.Contact{}, fmt.Errorf("group_id %d out of range", gid)
		}
		groupPtr = &gid
	}

	contact := model.Contact{
		FirstName:    first,
		LastName:     last,
		Phone:        normalized,
		AccountIndex: idx,
		GroupID:      groupPtr,
		Nickname:     strings.TrimSpace(rc.Nickname),
		SourcePath:   fd.Path,
		SourceMod:    fd.ModTime,
	}
	return contact, nil
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
		return "", fmt.Errorf("invalid character %q in phone", r)
	}
	normalized := b.String()
	if normalized == "" {
		return "", errors.New("phone empty after normalization")
	}
	return normalized, nil
}
