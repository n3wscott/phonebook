package asterisk

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/n3wscott/phonebook/internal/config"
	"github.com/n3wscott/phonebook/internal/model"
)

// RenderPJSIP builds pjsip.conf contents.
func RenderPJSIP(cfg config.Config, contacts []model.Contact) ([]byte, error) {
	var b strings.Builder

	writeSection(&b, "global", func() {
		writeKV(&b, "type", "global")
		writeOptions(&b, cfg.Global)
	})

	for _, transport := range cfg.Transports {
		section := transport.Name
		writeSection(&b, section, func() {
			writeKV(&b, "type", "transport")
			if transport.Protocol != "" {
				writeKV(&b, "protocol", transport.Protocol)
			}
			if transport.Bind != "" {
				writeKV(&b, "bind", transport.Bind)
			}
			writeNetworkDefaults(&b, cfg.Network, transport.Extra)
			writeMap(&b, transport.Extra)
		})
	}

	for _, tmpl := range cfg.EndpointTemplates {
		name := fmt.Sprintf("%s(!)", tmpl.Name)
		writeSection(&b, name, func() {
			writeKV(&b, "type", "endpoint")
			writeMap(&b, tmpl.Extra)
		})
	}

	for _, c := range contacts {
		fmt.Fprintf(&b, "\n; Auth & AOR for extension %s\n", c.Extension)
		writeSection(&b, fmt.Sprintf("%s(%s)", c.Extension, c.Endpoint.Template), func() {
			writeKV(&b, "type", "endpoint")
			writeKV(&b, "auth", c.Extension)
			writeKV(&b, "aors", c.Extension)
		})
		writeSection(&b, c.Extension, func() {
			writeKV(&b, "type", "auth")
			writeKV(&b, "auth_type", "userpass")
			writeKV(&b, "username", c.Auth.Username)
			writeKV(&b, "password", c.Auth.Password)
		})
		writeSection(&b, c.Extension, func() {
			writeKV(&b, "type", "aor")
			writeKV(&b, "max_contacts", c.AOR.MaxContacts)
			writeKV(&b, "remove_existing", c.AOR.RemoveExisting)
			writeKV(&b, "qualify_frequency", c.AOR.QualifyFrequency)
		})
	}

	b.WriteByte('\n')
	return []byte(b.String()), nil
}

// RenderExtensions builds extensions.conf.
func RenderExtensions(cfg config.Config, contacts []model.Contact) ([]byte, error) {
	var b strings.Builder
	context := cfg.Dialplan.Context
	if context == "" {
		context = "internal"
	}
	writeSection(&b, context, func() {
		for _, c := range contacts {
			fmt.Fprintf(&b, "exten => %s,1,Dial(PJSIP/%s)\n", c.Extension, c.Extension)
		}
	})
	b.WriteByte('\n')
	return []byte(b.String()), nil
}

func writeSection(b *strings.Builder, name string, fn func()) {
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	fmt.Fprintf(b, "[%s]\n", name)
	fn()
}

func writeNetworkDefaults(b *strings.Builder, net config.Network, overrides map[string]any) {
	if net.ExternalSignalingAddress != "" {
		if _, ok := overrides["external_signaling_address"]; !ok {
			writeKV(b, "external_signaling_address", net.ExternalSignalingAddress)
		}
	}
	if net.ExternalMediaAddress != "" {
		if _, ok := overrides["external_media_address"]; !ok {
			writeKV(b, "external_media_address", net.ExternalMediaAddress)
		}
	}
	if len(net.LocalNet) > 0 {
		if _, ok := overrides["local_net"]; !ok {
			for _, cidr := range net.LocalNet {
				writeKV(b, "local_net", cidr)
			}
		}
	}
}

func writeMap(b *strings.Builder, m map[string]any) {
	if len(m) == 0 {
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		writeKV(b, k, m[k])
	}
}

func writeOptions(b *strings.Builder, m map[string]any) {
	writeMap(b, m)
}

func writeKV(b *strings.Builder, key string, value any) {
	values := flatten(value)
	for _, v := range values {
		fmt.Fprintf(b, "%s=%s\n", key, v)
	}
}

func flatten(v any) []string {
	switch val := v.(type) {
	case nil:
		return nil
	case string:
		return []string{val}
	case fmt.Stringer:
		return []string{val.String()}
	case bool:
		if val {
			return []string{"yes"}
		}
		return []string{"no"}
	case int:
		return []string{strconv.Itoa(val)}
	case int64:
		return []string{strconv.FormatInt(val, 10)}
	case int32:
		return []string{strconv.FormatInt(int64(val), 10)}
	case uint:
		return []string{strconv.FormatUint(uint64(val), 10)}
	case uint32:
		return []string{strconv.FormatUint(uint64(val), 10)}
	case uint64:
		return []string{strconv.FormatUint(val, 10)}
	case float32:
		return []string{strconv.FormatFloat(float64(val), 'f', -1, 32)}
	case float64:
		return []string{strconv.FormatFloat(val, 'f', -1, 64)}
	case []string:
		return append([]string{}, val...)
	case []int:
		out := make([]string, 0, len(val))
		for _, n := range val {
			out = append(out, strconv.Itoa(n))
		}
		return out
	case []interface{}:
		var out []string
		for _, item := range val {
			out = append(out, flatten(item)...)
		}
		return out
	default:
		return []string{fmt.Sprint(val)}
	}
}
