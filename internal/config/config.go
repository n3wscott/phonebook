package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// FileMeta tracks source files for change detection.
type FileMeta struct {
	Path    string
	ModTime time.Time
}

// Config represents config.yaml.
type Config struct {
	Global            map[string]any   `yaml:"global"`
	Network           Network          `yaml:"network"`
	Transports        []Transport      `yaml:"transports"`
	EndpointTemplates []EndpointConfig `yaml:"endpoint_templates"`
	Dialplan          Dialplan         `yaml:"dialplan"`
	Server            Server           `yaml:"server"`
}

// Network aggregates transport-related addresses.
type Network struct {
	ExternalSignalingAddress string         `yaml:"external_signaling_address"`
	ExternalMediaAddress     string         `yaml:"external_media_address"`
	LocalNet                 []string       `yaml:"local_net"`
	Extra                    map[string]any `yaml:",inline"`
}

// Transport describes a pjsip transport section.
type Transport struct {
	Name     string         `yaml:"name"`
	Protocol string         `yaml:"protocol"`
	Bind     string         `yaml:"bind"`
	Extra    map[string]any `yaml:",inline"`
}

// EndpointConfig defines a template block for endpoints.
type EndpointConfig struct {
	Name  string         `yaml:"name"`
	Extra map[string]any `yaml:",inline"`
}

// Dialplan config.
type Dialplan struct {
	Context string `yaml:"context"`
}

// Server config section.
type Server struct {
	Addr     string `yaml:"addr"`
	BasePath string `yaml:"base_path"`
}

// Defaults defines repo-wide per-contact defaults.
type Defaults struct {
	AOR      AORDefaults
	Auth     AuthDefaults
	Endpoint EndpointDefaults
}

// AORDefaults applies to contact AOR blocks.
type AORDefaults struct {
	MaxContacts      int
	RemoveExisting   bool
	QualifyFrequency int
}

// AuthDefaults configures auth fallback behavior.
type AuthDefaults struct {
	UsernameEqualsExt bool
}

// EndpointDefaults selects the template to inherit.
type EndpointDefaults struct {
	Template string
}

var builtinDefaults = Defaults{
	AOR: AORDefaults{
		MaxContacts:      1,
		RemoveExisting:   true,
		QualifyFrequency: 30,
	},
	Auth: AuthDefaults{
		UsernameEqualsExt: true,
	},
	Endpoint: EndpointDefaults{
		Template: "endpoint-template",
	},
}

// Load reads config.yaml and defaults.yaml from dir.
func Load(dir string) (Config, Defaults, []FileMeta, error) {
	configPath := filepath.Join(dir, "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return Config{}, Defaults{}, nil, fmt.Errorf("read config.yaml: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, Defaults{}, nil, fmt.Errorf("parse config.yaml: %w", err)
	}
	cfg.normalize()

	metas := []FileMeta{}
	if info, err := os.Stat(configPath); err == nil {
		metas = append(metas, FileMeta{Path: configPath, ModTime: info.ModTime()})
	}

	defs := builtinDefaults
	defPath := filepath.Join(dir, "defaults.yaml")
	if raw, err := os.ReadFile(defPath); err == nil {
		var file defaultsFile
		if err := yaml.Unmarshal(raw, &file); err != nil {
			return Config{}, Defaults{}, nil, fmt.Errorf("parse defaults.yaml: %w", err)
		}
		defs = mergeDefaults(builtinDefaults, file)
		if info, err := os.Stat(defPath); err == nil {
			metas = append(metas, FileMeta{Path: defPath, ModTime: info.ModTime()})
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Config{}, Defaults{}, nil, fmt.Errorf("read defaults.yaml: %w", err)
	}

	if err := validate(cfg, defs); err != nil {
		return Config{}, Defaults{}, nil, err
	}

	return cfg, defs, metas, nil
}

func (c *Config) normalize() {
	if c.Global == nil {
		c.Global = map[string]any{}
	}
	if c.Network.Extra == nil {
		c.Network.Extra = map[string]any{}
	}
	for i := range c.Transports {
		if c.Transports[i].Extra == nil {
			c.Transports[i].Extra = map[string]any{}
		}
	}
	for i := range c.EndpointTemplates {
		if c.EndpointTemplates[i].Extra == nil {
			c.EndpointTemplates[i].Extra = map[string]any{}
		}
	}
	if c.Server.Addr == "" {
		c.Server.Addr = ":8080"
	}
	c.Server.BasePath = sanitizeBasePath(c.Server.BasePath)
	if c.Dialplan.Context == "" {
		c.Dialplan.Context = "internal"
	}
}

func sanitizeBasePath(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if !strings.HasSuffix(p, "/") {
		p = p + "/"
	}
	return pathCleanPreserve(p)
}

func pathCleanPreserve(p string) string {
	parts := strings.Split(p, "/")
	stack := []string{}
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			continue
		}
		stack = append(stack, part)
	}
	res := "/" + strings.Join(stack, "/")
	if p == "/" {
		return "/"
	}
	if strings.HasSuffix(p, "/") && !strings.HasSuffix(res, "/") {
		res += "/"
	}
	return res
}

type defaultsFile struct {
	AOR struct {
		MaxContacts      *int  `yaml:"max_contacts"`
		RemoveExisting   *bool `yaml:"remove_existing"`
		QualifyFrequency *int  `yaml:"qualify_frequency"`
	} `yaml:"aor"`
	Auth struct {
		UsernameEqualsExt *bool `yaml:"username_equals_ext"`
	} `yaml:"auth"`
	Endpoint struct {
		Template *string `yaml:"template"`
	} `yaml:"endpoint"`
}

func mergeDefaults(base Defaults, override defaultsFile) Defaults {
	out := base
	if override.AOR.MaxContacts != nil {
		out.AOR.MaxContacts = *override.AOR.MaxContacts
	}
	if override.AOR.QualifyFrequency != nil {
		out.AOR.QualifyFrequency = *override.AOR.QualifyFrequency
	}
	if override.AOR.RemoveExisting != nil {
		out.AOR.RemoveExisting = *override.AOR.RemoveExisting
	}
	if override.Auth.UsernameEqualsExt != nil {
		out.Auth.UsernameEqualsExt = *override.Auth.UsernameEqualsExt
	}
	if override.Endpoint.Template != nil {
		out.Endpoint.Template = *override.Endpoint.Template
	}
	return out
}

func validate(cfg Config, defs Defaults) error {
	if len(cfg.Transports) == 0 {
		return errors.New("config.yaml must define at least one transport")
	}
	if defs.Endpoint.Template == "" {
		return errors.New("defaults endpoint template is required")
	}

	names := map[string]struct{}{}
	for _, tmpl := range cfg.EndpointTemplates {
		if tmpl.Name == "" {
			return errors.New("endpoint template missing name")
		}
		names[tmpl.Name] = struct{}{}
	}
	if _, ok := names[defs.Endpoint.Template]; !ok {
		return fmt.Errorf("endpoint template %q referenced by defaults not found in config.yaml", defs.Endpoint.Template)
	}
	return nil
}

// TemplateNames returns configured template names.
func (c Config) TemplateNames() []string {
	out := make([]string, 0, len(c.EndpointTemplates))
	for _, t := range c.EndpointTemplates {
		out = append(out, t.Name)
	}
	sort.Strings(out)
	return out
}
