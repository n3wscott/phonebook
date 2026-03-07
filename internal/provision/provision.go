package provision

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/n3wscott/phonebook/internal/config"
	"gopkg.in/yaml.v3"
)

// Options controls rendered provisioning values.
type Options struct {
	SIPServer         string
	SIPPort           string
	OutboundProxy     string
	PhonebookURL      string
	ProvisionURL      string
	ProvisionHostport string
	TemplatesDir      string
	DefaultTemplate   string
}

// Build renders cfg<mac>.xml files for users found under <dir>/users.
// Missing users/templates directories return empty output (not an error).
func Build(dir string, opts Options) (map[string][]byte, []config.FileMeta, error) {
	usersDir := filepath.Join(dir, "users")
	if stat, err := os.Stat(usersDir); err != nil {
		if os.IsNotExist(err) {
			return map[string][]byte{}, nil, nil
		}
		return nil, nil, err
	} else if !stat.IsDir() {
		return nil, nil, fmt.Errorf("users path is not a directory: %s", usersDir)
	}

	templatesDir := opts.TemplatesDir
	if templatesDir == "" {
		templatesDir = filepath.Join(dir, "provisioning", "templates")
	}
	if _, err := os.Stat(templatesDir); err != nil {
		if os.IsNotExist(err) {
			return map[string][]byte{}, nil, nil
		}
		return nil, nil, err
	}
	if opts.DefaultTemplate == "" {
		opts.DefaultTemplate = "wp816.xml.tmpl"
	}

	entries, err := os.ReadDir(usersDir)
	if err != nil {
		return nil, nil, err
	}

	provision := map[string][]byte{}
	metas := make([]config.FileMeta, 0, len(entries)*2)
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		if !strings.HasSuffix(name, ".yaml") || name == "_template.yaml" || name == "config.yaml" {
			continue
		}
		userPath := filepath.Join(usersDir, name)
		raw, err := os.ReadFile(userPath)
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", userPath, err)
		}
		var user userEntry
		if err := yaml.Unmarshal(raw, &user); err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", userPath, err)
		}

		mac := normalizeMAC(user.MACAddress)
		ext := strings.TrimSpace(user.SIPExtension)
		if len(mac) != 12 || ext == "" {
			continue
		}

		templatePath, err := resolveTemplatePath(templatesDir, opts.DefaultTemplate, user.PhoneType)
		if err != nil {
			continue
		}
		templateRaw, err := os.ReadFile(templatePath)
		if err != nil {
			return nil, nil, fmt.Errorf("read template %s: %w", templatePath, err)
		}

		firstName, lastName := splitName(user.KidAssigned)
		values := map[string]string{
			"phone_type":              user.PhoneType,
			"serial_number":           user.SerialNumber,
			"mac_address":             mac,
			"mac_address_raw":         user.MACAddress,
			"original_phone_password": user.OriginalPhonePassword,
			"updated_phone_password":  user.UpdatedPhonePassword,
			"kid_assigned":            user.KidAssigned,
			"first_name":              firstName,
			"last_name":               lastName,
			"sip_extension":           ext,
			"sip_password":            user.SIPPassword,
			"grade":                   user.Grade,
			"sip_server":              opts.SIPServer,
			"sip_port":                opts.SIPPort,
			"outbound_proxy":          opts.OutboundProxy,
			"phonebook_url":           opts.PhonebookURL,
			"provision_url":           opts.ProvisionURL,
			"provision_hostport":      opts.ProvisionHostport,
		}
		rendered := renderTemplate(string(templateRaw), values)
		provision["cfg"+mac+".xml"] = []byte(rendered)

		if st, err := os.Stat(userPath); err == nil {
			metas = append(metas, config.FileMeta{Path: userPath, ModTime: st.ModTime()})
		}
		if st, err := os.Stat(templatePath); err == nil {
			metas = append(metas, config.FileMeta{Path: templatePath, ModTime: st.ModTime()})
		}
	}

	sort.Slice(metas, func(i, j int) bool { return metas[i].Path < metas[j].Path })
	return provision, metas, nil
}

type userEntry struct {
	PhoneType             string `yaml:"phone_type"`
	SerialNumber          string `yaml:"serial_number"`
	MACAddress            string `yaml:"mac_address"`
	OriginalPhonePassword string `yaml:"original_phone_password"`
	UpdatedPhonePassword  string `yaml:"updated_phone_password"`
	KidAssigned           string `yaml:"kid_assigned"`
	SIPExtension          string `yaml:"sip_extension"`
	SIPPassword           string `yaml:"sip_password"`
	Grade                 string `yaml:"grade"`
}

var tokenRe = regexp.MustCompile(`\{\{([a-zA-Z0-9_]+)\}\}`)

func renderTemplate(tmpl string, values map[string]string) string {
	return tokenRe.ReplaceAllStringFunc(tmpl, func(tok string) string {
		matches := tokenRe.FindStringSubmatch(tok)
		if len(matches) != 2 {
			return ""
		}
		return values[matches[1]]
	})
}

func normalizeMAC(in string) string {
	in = strings.ToLower(in)
	var b strings.Builder
	b.Grow(len(in))
	for _, r := range in {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func splitName(full string) (string, string) {
	parts := strings.Fields(strings.TrimSpace(full))
	if len(parts) == 0 {
		return "", ""
	}
	if len(parts) == 1 {
		return parts[0], ""
	}
	return strings.Join(parts[:len(parts)-1], " "), parts[len(parts)-1]
}

func resolveTemplatePath(templatesDir, defaultTemplate, phoneType string) (string, error) {
	candidates := []string{}
	pt := normalizeName(phoneType)
	if pt != "" {
		candidates = append(candidates, pt+".xml.tmpl")
	}
	if strings.Contains(strings.ToLower(phoneType), "wp816") {
		candidates = append(candidates, "wp816.xml.tmpl")
	}
	if defaultTemplate != "" {
		candidates = append(candidates, defaultTemplate)
	}

	seen := map[string]bool{}
	for _, c := range candidates {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		path := filepath.Join(templatesDir, c)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", os.ErrNotExist
}

func normalizeName(in string) string {
	in = strings.ToLower(strings.TrimSpace(in))
	in = strings.ReplaceAll(in, "-", " ")
	in = strings.ReplaceAll(in, "/", " ")
	parts := strings.Fields(in)
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "_")
}
