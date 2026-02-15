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

	// Build a lookup for static AOR contacts, keyed by extension.
	staticContactByExt := map[string]string{}
	if len(cfg.Asterisk.StaticContacts) > 0 {
		for _, sc := range cfg.Asterisk.StaticContacts {
			if sc.Ext != "" && sc.Contact != "" {
				staticContactByExt[sc.Ext] = sc.Contact
			}
		}
	}

	writeSection(&b, "global", func() {
		writeKV(&b, "type", "global")
		writeOptions(&b, cfg.Global)
		if _, ok := cfg.Global["endpoint_identifier_order"]; !ok {
			writeKV(&b, "endpoint_identifier_order", "username,ip,anonymous")
		}
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
		writeTemplateSection(&b, tmpl.Name, func() {
			writeKV(&b, "type", "endpoint")
			writeEndpointOptions(&b, tmpl.Extra)
		})
	}

	// Optional trusted inbound edge endpoint (no auth) identified by source IP.
	if cfg.Asterisk.EdgeIn.Match != "" {
		name := cfg.Asterisk.EdgeIn.Name
		if name == "" {
			name = "edge-in"
		}
		inboundCtx := cfg.Asterisk.EdgeIn.Context
		if inboundCtx == "" {
			inboundCtx = cfg.Dialplan.Context
			if inboundCtx == "" {
				inboundCtx = "internal"
			}
		}
		// Render a minimal endpoint suitable for edge proxy ingress.
		writeSection(&b, name, func() {
			writeKV(&b, "type", "endpoint")
			// Security model: no auth; identify by IP below.
			writeKV(&b, "context", inboundCtx)
			writeKV(&b, "disallow", "all")
			// Allow codecs: reuse first endpoint template allow if available; else default.
			allowed := defaultAllowFromTemplates(cfg)
			for _, c := range allowed {
				writeKV(&b, "allow", c)
			}
			// NAT-friendly defaults matching templates
			writeKV(&b, "direct_media", "no")
			writeKV(&b, "rtp_symmetric", "yes")
			writeKV(&b, "force_rport", "yes")
			writeKV(&b, "rewrite_contact", "yes")
			// Use first transport name if defined
			if len(cfg.Transports) > 0 {
				writeKV(&b, "transport", cfg.Transports[0].Name)
			}
		})
		// Identify mapping for the edge source IP/host to the endpoint.
		writeSection(&b, name, func() {
			writeKV(&b, "type", "identify")
			writeKV(&b, "endpoint", name)
			writeKV(&b, "match", cfg.Asterisk.EdgeIn.Match)
		})
	}

	for _, c := range contacts {
		fmt.Fprintf(&b, "\n; Auth & AOR for extension %s\n", c.Extension)
		writeInheritedSection(&b, c.Extension, c.Endpoint.Template, func() {
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
			if uri, ok := staticContactByExt[c.Extension]; ok {
				writeKV(&b, "contact", uri)
			}
		})
	}

	b.WriteByte('\n')
	return []byte(b.String()), nil
}

// defaultAllowFromTemplates returns the allow list from the first endpoint template,
// or a safe fallback.
func defaultAllowFromTemplates(cfg config.Config) []string {
	if len(cfg.EndpointTemplates) > 0 {
		if v, ok := cfg.EndpointTemplates[0].Extra["allow"]; ok {
			return flatten(v)
		}
	}
	return []string{"ulaw", "alaw", "g722"}
}

// RenderExtensions builds extensions.conf.
func RenderExtensions(cfg config.Config, contacts []model.Contact) ([]byte, error) {
	var b strings.Builder
	mainContext := cfg.Dialplan.Context
	if mainContext == "" {
		mainContext = "internal"
	}

	conferenceByContext := map[string][]config.Conference{}
	conferenceContextOrder := []string{}
	seenConferenceContext := map[string]struct{}{}
	for _, conference := range cfg.Dialplan.Conferences {
		ctx := conference.Context
		if ctx == "" {
			ctx = "conferences"
		}
		if conference.Room == "" {
			conference.Room = conference.Extension
		}
		conference.Context = ctx
		if _, ok := seenConferenceContext[ctx]; !ok {
			seenConferenceContext[ctx] = struct{}{}
			conferenceContextOrder = append(conferenceContextOrder, ctx)
		}
		conferenceByContext[ctx] = append(conferenceByContext[ctx], conference)
	}

	messageContext := cfg.Dialplan.Messages.Context
	if messageContext == "" {
		messageContext = "messages"
	}
	messagePattern := cfg.Dialplan.Messages.Pattern
	if messagePattern == "" {
		messagePattern = "_X."
	}

	includes := []string{}
	seenIncludes := map[string]struct{}{}
	addInclude := func(name string) {
		if name == "" || name == mainContext {
			return
		}
		if _, ok := seenIncludes[name]; ok {
			return
		}
		seenIncludes[name] = struct{}{}
		includes = append(includes, name)
	}

	for _, include := range cfg.Dialplan.Includes {
		addInclude(include)
	}
	for _, context := range conferenceContextOrder {
		addInclude(context)
	}
	if cfg.Dialplan.Messages.Enabled {
		addInclude(messageContext)
	}

	writeSection(&b, mainContext, func() {
		for _, include := range includes {
			fmt.Fprintf(&b, "include => %s\n", include)
		}
		for _, c := range contacts {
			fmt.Fprintf(&b, "exten => %s,1,Dial(PJSIP/%s)\n", c.Extension, c.Extension)
		}
		for _, conference := range conferenceByContext[mainContext] {
			writeConferenceExtension(&b, conference)
		}
		if cfg.Dialplan.Messages.Enabled && messageContext == mainContext {
			writeMessageRouting(&b, messagePattern)
		}
	})

	for _, context := range conferenceContextOrder {
		if context == mainContext {
			continue
		}
		writeSection(&b, context, func() {
			for _, conference := range conferenceByContext[context] {
				writeConferenceExtension(&b, conference)
			}
		})
	}

	if cfg.Dialplan.Messages.Enabled && messageContext != mainContext {
		writeSection(&b, messageContext, func() {
			writeMessageRouting(&b, messagePattern)
		})
	}

	b.WriteByte('\n')
	return []byte(b.String()), nil
}

func writeConferenceExtension(b *strings.Builder, conference config.Conference) {
	fmt.Fprintf(b, "exten => %s,1,Answer()\n", conference.Extension)
	fmt.Fprintf(b, " same => n,ConfBridge(%s)\n", conference.Room)
	fmt.Fprintln(b, " same => n,Hangup()")
}

func writeMessageRouting(b *strings.Builder, pattern string) {
	fmt.Fprintf(b, "exten => %s,1,NoOp(Incoming SIP MESSAGE)\n", pattern)
	fmt.Fprintln(b, " same => n,MessageSend(pjsip:${EXTEN},${MESSAGE(from)})")
	fmt.Fprintln(b, " same => n,Hangup()")
}

func writeSection(b *strings.Builder, name string, fn func()) {
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	fmt.Fprintf(b, "[%s]\n", name)
	fn()
}

func writeTemplateSection(b *strings.Builder, name string, fn func()) {
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	fmt.Fprintf(b, "[%s](!)\n", name)
	fn()
}

func writeEndpointOptions(b *strings.Builder, m map[string]any) {
	if len(m) == 0 {
		return
	}
	// Ensure disallow is written before allow entries.
	if val, ok := m["disallow"]; ok {
		writeKV(b, "disallow", val)
	}
	if val, ok := m["allow"]; ok {
		writeKV(b, "allow", val)
	}
	for key, val := range m {
		if key == "allow" || key == "disallow" {
			continue
		}
		writeKV(b, key, val)
	}
}

func writeInheritedSection(b *strings.Builder, name, template string, fn func()) {
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	fmt.Fprintf(b, "[%s](%s)\n", name, template)
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
