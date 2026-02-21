package httpapi

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/n3wscott/phonebook/internal/model"
)

type dashboardCall struct {
	ID          string    `json:"id"`
	From        string    `json:"from"`
	FromName    string    `json:"from_name,omitempty"`
	To          string    `json:"to"`
	ToName      string    `json:"to_name,omitempty"`
	State       string    `json:"state"`
	EndReason   string    `json:"end_reason,omitempty"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end,omitempty"`
	DurationSec int64     `json:"duration_sec"`
}

type dashboardPayload struct {
	GeneratedAt time.Time          `json:"generated_at"`
	Active      []dashboardCall    `json:"active"`
	History     []dashboardCall    `json:"history"`
	Contacts    []dashboardContact `json:"contacts"`
}

type dashboardContact struct {
	ID      string    `json:"id"`
	Name    string    `json:"name,omitempty"`
	State   string    `json:"state"`
	Detail  string    `json:"detail,omitempty"`
	Updated time.Time `json:"updated,omitempty"`
	Known   bool      `json:"known"`
}

func (s *Server) handleCallsPage(w http.ResponseWriter, _ *http.Request) {
	if s.calls == nil {
		http.Error(w, "calls dashboard disabled", http.StatusServiceUnavailable)
		return
	}
	wsPath := s.join("calls/ws")
	activePath := s.join("api/calls/active")
	historyPath := s.join("api/calls/history")
	contactsPath := s.join("api/calls/contacts")

	page := fmt.Sprintf(callsDashboardHTML, wsPath, activePath, historyPath, contactsPath)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(page))
}

func (s *Server) handleCallsActive(w http.ResponseWriter, _ *http.Request) {
	if s.calls == nil {
		http.Error(w, "calls dashboard disabled", http.StatusServiceUnavailable)
		return
	}
	payload := s.buildCallsPayload()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"generated_at": payload.GeneratedAt,
		"active":       payload.Active,
	})
}

func (s *Server) handleCallsHistory(w http.ResponseWriter, _ *http.Request) {
	if s.calls == nil {
		http.Error(w, "calls dashboard disabled", http.StatusServiceUnavailable)
		return
	}
	payload := s.buildCallsPayload()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"generated_at": payload.GeneratedAt,
		"history":      payload.History,
	})
}

func (s *Server) handleCallsContacts(w http.ResponseWriter, _ *http.Request) {
	if s.calls == nil {
		http.Error(w, "calls dashboard disabled", http.StatusServiceUnavailable)
		return
	}
	payload := s.buildCallsPayload()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"generated_at": payload.GeneratedAt,
		"contacts":     payload.Contacts,
	})
}

func (s *Server) handleCallsWS(w http.ResponseWriter, r *http.Request) {
	if s.calls == nil {
		http.Error(w, "calls dashboard disabled", http.StatusServiceUnavailable)
		return
	}
	conn, err := upgradeWebSocket(w, r)
	if err != nil {
		return
	}
	defer conn.Close()

	sub, cancel := s.calls.Subscribe()
	defer cancel()

	if err := s.writeCallsPayloadFrame(conn); err != nil {
		return
	}

	pingTicker := time.NewTicker(25 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub:
			if err := s.writeCallsPayloadFrame(conn); err != nil {
				return
			}
		case <-pingTicker.C:
			if err := writeWebSocketFrame(conn, 0x9, nil); err != nil {
				return
			}
		}
	}
}

func (s *Server) writeCallsPayloadFrame(conn net.Conn) error {
	payload := s.buildCallsPayload()
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return writeWebSocketFrame(conn, 0x1, data)
}

func (s *Server) buildCallsPayload() dashboardPayload {
	callSnapshot := s.calls.Snapshot()
	phonebookSnapshot, _ := s.currentSnapshot()
	nameLookup := buildNameLookup(phonebookSnapshot.Contacts)

	active := make([]dashboardCall, 0, len(callSnapshot.Active))
	for _, call := range callSnapshot.Active {
		fromParty := canonicalParty(call.From)
		toParty := canonicalParty(call.To)
		active = append(active, dashboardCall{
			ID:          call.ID,
			From:        fromParty,
			FromName:    resolveName(nameLookup, fromParty),
			To:          toParty,
			ToName:      resolveName(nameLookup, toParty),
			State:       call.State,
			Start:       call.Start,
			DurationSec: int64(time.Since(call.Start).Seconds()),
		})
	}

	history := make([]dashboardCall, 0, len(callSnapshot.History))
	for _, call := range callSnapshot.History {
		fromParty := canonicalParty(call.From)
		toParty := canonicalParty(call.To)
		history = append(history, dashboardCall{
			ID:          call.ID,
			From:        fromParty,
			FromName:    resolveName(nameLookup, fromParty),
			To:          toParty,
			ToName:      resolveName(nameLookup, toParty),
			State:       call.State,
			EndReason:   call.EndReason,
			Start:       call.Start,
			End:         call.End,
			DurationSec: call.DurationSec,
		})
	}

	contactByID := make(map[string]dashboardContact)
	aliasToID := make(map[string]string)
	for _, contact := range phonebookSnapshot.Contacts {
		id := canonicalParty(contact.Extension)
		if id == "" {
			for _, phone := range contact.Phones {
				id = canonicalParty(phone.Number)
				if id != "" {
					break
				}
			}
		}
		if id == "" {
			continue
		}
		name := strings.TrimSpace(contact.FirstName + " " + contact.LastName)
		if name == "" {
			name = resolveName(nameLookup, id)
		}
		if name == "" {
			name = id
		}
		if _, exists := contactByID[id]; !exists {
			contactByID[id] = dashboardContact{
				ID:     id,
				Name:   name,
				State:  "disconnected",
				Detail: "no endpoint presence yet",
				Known:  true,
			}
		}
		aliasToID[id] = id
		if extAlias := canonicalParty(contact.Extension); extAlias != "" {
			aliasToID[extAlias] = id
		}
		for _, phone := range contact.Phones {
			if phoneAlias := canonicalParty(phone.Number); phoneAlias != "" {
				aliasToID[phoneAlias] = id
			}
		}
	}

	for _, p := range callSnapshot.Presences {
		id := canonicalParty(p.ID)
		if id == "" {
			continue
		}
		targetID := id
		if mappedID, ok := aliasToID[id]; ok {
			targetID = mappedID
		}
		current := contactByID[targetID]
		name := current.Name
		if name == "" {
			name = resolveName(nameLookup, targetID)
		}
		contactByID[targetID] = dashboardContact{
			ID:      targetID,
			Name:    name,
			State:   p.State,
			Detail:  p.Detail,
			Updated: p.Updated,
			Known:   current.Known,
		}
	}
	contacts := make([]dashboardContact, 0, len(contactByID))
	for _, c := range contactByID {
		contacts = append(contacts, c)
	}
	sort.Slice(contacts, func(i, j int) bool {
		if contacts[i].Known != contacts[j].Known {
			return contacts[i].Known
		}
		wi := contactStateWeight(contacts[i].State)
		wj := contactStateWeight(contacts[j].State)
		if wi != wj {
			return wi < wj
		}
		if contacts[i].Name != contacts[j].Name {
			return contacts[i].Name < contacts[j].Name
		}
		return contacts[i].ID < contacts[j].ID
	})

	return dashboardPayload{
		GeneratedAt: time.Now().UTC(),
		Active:      active,
		History:     history,
		Contacts:    contacts,
	}
}

func contactStateWeight(state string) int {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "in-use", "in use", "in-call":
		return 0
	case "connected", "not in use", "online", "active":
		return 1
	case "disconnected", "offline", "unknown":
		return 2
	default:
		return 3
	}
}

func buildNameLookup(contacts []model.Contact) map[string]string {
	lookup := make(map[string]string, len(contacts)*2)
	for _, contact := range contacts {
		name := strings.TrimSpace(contact.FirstName + " " + contact.LastName)
		if name == "" {
			continue
		}
		addLookupEntry(lookup, contact.Extension, name)
		for _, phone := range contact.Phones {
			addLookupEntry(lookup, phone.Number, name)
		}
	}
	return lookup
}

func addLookupEntry(lookup map[string]string, key, value string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	if _, exists := lookup[key]; !exists {
		lookup[key] = value
	}
	normalized := normalizeNumber(key)
	if normalized != "" {
		if _, exists := lookup[normalized]; !exists {
			lookup[normalized] = value
		}
	}
}

func resolveName(lookup map[string]string, raw string) string {
	if raw == "" {
		return ""
	}
	if name, ok := lookup[raw]; ok {
		return name
	}
	if name, ok := lookup[normalizeNumber(raw)]; ok {
		return name
	}
	return ""
}

func normalizeNumber(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if (r >= '0' && r <= '9') || r == '+' || r == '*' || r == '#' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func canonicalParty(raw string) string {
	clean := normalizeNumber(raw)
	if clean != "" {
		return clean
	}
	return strings.TrimSpace(raw)
}

func upgradeWebSocket(w http.ResponseWriter, r *http.Request) (net.Conn, error) {
	if !headerHasToken(r.Header.Get("Connection"), "upgrade") || !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		http.Error(w, "websocket upgrade required", http.StatusBadRequest)
		return nil, fmt.Errorf("invalid websocket upgrade request")
	}
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-WebSocket-Version")), "13") {
		http.Error(w, "unsupported websocket version", http.StatusBadRequest)
		return nil, fmt.Errorf("unsupported websocket version")
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		http.Error(w, "missing websocket key", http.StatusBadRequest)
		return nil, fmt.Errorf("missing websocket key")
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket not supported", http.StatusInternalServerError)
		return nil, fmt.Errorf("response writer does not support hijacking")
	}

	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return nil, err
	}

	accept := websocketAcceptKey(key)
	_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	_, _ = rw.WriteString("Upgrade: websocket\r\n")
	_, _ = rw.WriteString("Connection: Upgrade\r\n")
	_, _ = rw.WriteString("Sec-WebSocket-Accept: " + accept + "\r\n")
	_, _ = rw.WriteString("\r\n")
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func headerHasToken(value, token string) bool {
	for _, part := range strings.Split(value, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

func websocketAcceptKey(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func writeWebSocketFrame(conn net.Conn, opcode byte, payload []byte) error {
	header := []byte{0x80 | (opcode & 0x0f)}
	length := len(payload)
	switch {
	case length <= 125:
		header = append(header, byte(length))
	case length <= 65535:
		header = append(header, 126, byte(length>>8), byte(length))
	default:
		header = append(header, 127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[len(header)-8:], uint64(length))
	}
	if _, err := conn.Write(header); err != nil {
		return err
	}
	if length == 0 {
		return nil
	}
	_, err := conn.Write(payload)
	return err
}

const callsDashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Asterisk Calls</title>
  <style>
    :root{
      --bg:#f2f5f9;
      --card:#ffffff;
      --text:#0f172a;
      --muted:#5b6472;
      --accent:#166534;
      --warn:#ca8a04;
      --danger:#b91c1c;
      --line:#d8dee7;
      --history:#fff7ed;
    }
    body{
      margin:0;
      color:var(--text);
      background:
        radial-gradient(circle at 0%% 0%%, #dbeafe, transparent 45%%),
        radial-gradient(circle at 100%% 100%%, #dcfce7, transparent 45%%),
        var(--bg);
      font-family:"Avenir Next", "Segoe UI", sans-serif;
    }
    .wrap{
      max-width:1100px;
      margin:0 auto;
      padding:1.2rem;
    }
    .head{
      display:flex;
      justify-content:space-between;
      align-items:baseline;
      margin-bottom:1rem;
    }
    .title{
      margin:0;
      font-size:1.8rem;
      letter-spacing:0.02em;
      text-transform:uppercase;
    }
    .stamp{
      color:var(--muted);
      font-size:0.9rem;
    }
    .grid{
      display:grid;
      gap:1rem;
      grid-template-columns:repeat(3,minmax(0,1fr));
    }
    @media (max-width: 1100px){
      .grid{ grid-template-columns:repeat(2,minmax(0,1fr)); }
    }
    @media (max-width: 760px){
      .grid{ grid-template-columns:1fr; }
    }
    .panel{
      background:var(--card);
      border:1px solid var(--line);
      border-radius:14px;
      overflow:hidden;
      box-shadow:0 8px 18px rgba(15,23,42,0.06);
    }
    .panel h2{
      margin:0;
      padding:0.9rem 1rem;
      font-size:1rem;
      text-transform:uppercase;
      letter-spacing:0.05em;
      border-bottom:1px solid var(--line);
      background:#f8fafc;
    }
    .panel.history h2{
      background:var(--history);
    }
    ul{
      list-style:none;
      margin:0;
      padding:0;
      max-height:72vh;
      overflow:auto;
    }
    li{
      padding:0.75rem 1rem;
      border-bottom:1px solid var(--line);
      display:grid;
      gap:0.2rem;
      animation:fadein 160ms ease;
    }
    li:last-child{border-bottom:0;}
    .meta{
      color:var(--muted);
      font-size:0.82rem;
      display:flex;
      justify-content:space-between;
      gap:0.8rem;
    }
    .parties{
      font-weight:600;
      display:flex;
      flex-wrap:wrap;
      gap:0.45rem;
      align-items:center;
    }
    .badge{
      color:#fff;
      background:var(--accent);
      border-radius:999px;
      font-size:0.72rem;
      padding:0.12rem 0.5rem;
      text-transform:uppercase;
      letter-spacing:0.03em;
    }
    .badge.status-answered{ background:var(--accent); }
    .badge.status-no-answer{ background:var(--warn); }
    .badge.status-error{ background:var(--danger); }
    .badge.status-connected{ background:#15803d; }
    .badge.status-disconnected{ background:#b91c1c; }
    .badge.status-in-use{ background:#ca8a04; }
    .badge.status-in-call{ background:#1d4ed8; }
    .empty{
      color:var(--muted);
      padding:1rem;
      font-style:italic;
    }
    @keyframes fadein{
      from{opacity:0; transform:translateY(2px);}
      to{opacity:1; transform:translateY(0);}
    }
  </style>
</head>
<body>
  <div class="wrap">
    <div class="head">
      <h1 class="title">Asterisk Calls</h1>
      <div class="stamp" id="stamp">waiting for updates...</div>
    </div>
    <div class="grid">
      <section class="panel">
        <h2>Active</h2>
        <ul id="active"></ul>
      </section>
      <section class="panel history">
        <h2>History (last 100 / 7d)</h2>
        <ul id="history"></ul>
      </section>
      <section class="panel">
        <h2>Presence</h2>
        <ul id="contacts"></ul>
      </section>
    </div>
  </div>
  <script>
    const wsPath = %q;
    const activeApi = %q;
    const historyApi = %q;
    const contactsApi = %q;
    const wsScheme = location.protocol === "https:" ? "wss://" : "ws://";
    const wsURL = wsScheme + location.host + wsPath;
    const activeEl = document.getElementById("active");
    const historyEl = document.getElementById("history");
    const contactsEl = document.getElementById("contacts");
    const stampEl = document.getElementById("stamp");
    let pollTimer = null;

    function label(name, number) {
      if (name && number) return name + " (" + number + ")";
      return name || number || "unknown";
    }

    function fmtWhen(ts) {
      if (!ts) return "";
      return new Date(ts).toLocaleString();
    }

    function statusForCall(call, isHistory) {
      const state = String(call.state || "").toLowerCase();
      const reason = String(call.end_reason || "").toLowerCase();
      const source = state + " " + reason;

      if (!isHistory) {
        if (state === "active" || state === "up") {
          return { label: "Active", className: "status-answered" };
        }
        if (state === "ringing" || state === "dialing") {
          return { label: call.state || "Ringing", className: "status-no-answer" };
        }
        return { label: call.state || "Active", className: "status-no-answer" };
      }

      if (source.includes("404") || source.includes("not found") || source.includes("error") || source.includes("failed") || source.includes("failure") || source.includes("congestion") || source.includes("unavailable") || source.includes("reject")) {
        return { label: "Error", className: "status-error" };
      }
      if (source.includes("no answer") || source.includes("busy") || source.includes("cancel") || source.includes("timeout")) {
        return { label: "No Answer", className: "status-no-answer" };
      }
      if (source.includes("answered") || source.includes("normal clearing") || source.includes("completed")) {
        return { label: "Answered", className: "status-answered" };
      }
      return { label: "No Answer", className: "status-no-answer" };
    }

    function statusForContact(contact) {
      const state = String(contact.state || "").toLowerCase();
      if (state === "in-call" || state === "in-use" || state === "in use" || state === "busy" || state === "ringing" || state === "dialing") {
        return { label: "In Use", className: "status-in-use" };
      }
      if (state === "connected" || state === "not in use" || state === "online" || state === "active") {
        return { label: "Connected", className: "status-connected" };
      }
      return { label: "Disconnected", className: "status-disconnected" };
    }

    function renderList(el, calls, emptyText, isHistory) {
      el.innerHTML = "";
      if (!calls || calls.length === 0) {
        const item = document.createElement("li");
        item.className = "empty";
        item.textContent = emptyText;
        el.appendChild(item);
        return;
      }
      calls.forEach((call) => {
        const li = document.createElement("li");
        const status = statusForCall(call, isHistory);
        const parties = document.createElement("div");
        parties.className = "parties";
        const from = document.createElement("span");
        from.textContent = label(call.from_name, call.from);
        const arrow = document.createElement("span");
        arrow.innerHTML = "&rarr;";
        const to = document.createElement("span");
        to.textContent = label(call.to_name, call.to);
        const badge = document.createElement("span");
        badge.className = "badge " + status.className;
        badge.textContent = status.label;
        parties.appendChild(from);
        parties.appendChild(arrow);
        parties.appendChild(to);
        parties.appendChild(badge);
        const meta = document.createElement("div");
        meta.className = "meta";
        const left = document.createElement("span");
        left.textContent = isHistory ? "Ended: " + fmtWhen(call.end) : "Started: " + fmtWhen(call.start);
        const right = document.createElement("span");
        right.textContent = "Duration: " + (call.duration_sec || 0) + "s";
        meta.appendChild(left);
        meta.appendChild(right);
        li.appendChild(parties);
        li.appendChild(meta);
        if (isHistory && call.end_reason) {
          const reason = document.createElement("div");
          reason.className = "meta";
          reason.textContent = "Reason: " + call.end_reason;
          li.appendChild(reason);
        }
        el.appendChild(li);
      });
    }

    function applyPayload(payload) {
      renderList(activeEl, payload.active, "No active calls right now.", false);
      renderList(historyEl, payload.history, "No historical calls available.", true);
      renderContacts(contactsEl, payload.contacts || []);
      if (payload.generated_at) {
        stampEl.textContent = "updated " + new Date(payload.generated_at).toLocaleTimeString();
      }
    }

    function renderContacts(el, contacts) {
      el.innerHTML = "";
      if (!contacts || contacts.length === 0) {
        const item = document.createElement("li");
        item.className = "empty";
        item.textContent = "No presence data yet.";
        el.appendChild(item);
        return;
      }
      contacts.forEach((contact) => {
        const li = document.createElement("li");
        const parties = document.createElement("div");
        parties.className = "parties";
        const who = document.createElement("span");
        const identity = contact.name ? contact.name + " (" + (contact.id || "unknown") + ")" : (contact.id || "unknown");
        who.textContent = identity;
        const badge = document.createElement("span");
        const status = statusForContact(contact);
        badge.className = "badge " + status.className;
        badge.textContent = status.label;
        parties.appendChild(who);
        parties.appendChild(badge);
        li.appendChild(parties);

        const meta = document.createElement("div");
        meta.className = "meta";
        const left = document.createElement("span");
        if (contact.detail) {
          left.textContent = "Detail: " + contact.detail;
        } else if (!contact.known) {
          left.textContent = "Not in phonebook";
        } else {
          left.textContent = "";
        }
        const right = document.createElement("span");
        right.textContent = contact.updated ? ("Seen: " + fmtWhen(contact.updated)) : "";
        meta.appendChild(left);
        meta.appendChild(right);
        li.appendChild(meta);
        el.appendChild(li);
      });
    }

    async function fallbackPoll() {
      try {
        const [activeRes, historyRes, contactsRes] = await Promise.all([fetch(activeApi), fetch(historyApi), fetch(contactsApi)]);
        const activeJson = await activeRes.json();
        const historyJson = await historyRes.json();
        const contactsJson = await contactsRes.json();
        applyPayload({
          generated_at: activeJson.generated_at || historyJson.generated_at || contactsJson.generated_at,
          active: activeJson.active || [],
          history: historyJson.history || [],
          contacts: contactsJson.contacts || []
        });
      } catch (err) {
        stampEl.textContent = "polling error";
      }
    }

    function startPolling() {
      if (pollTimer !== null) return;
      pollTimer = setInterval(fallbackPoll, 10000);
    }

    function stopPolling() {
      if (pollTimer === null) return;
      clearInterval(pollTimer);
      pollTimer = null;
    }

    function startWebSocket() {
      const ws = new WebSocket(wsURL);
      ws.onmessage = (event) => {
        try {
          const payload = JSON.parse(event.data);
          applyPayload(payload);
        } catch (_) {
          stampEl.textContent = "invalid update payload";
        }
      };
      ws.onopen = () => {
        stampEl.textContent = "live connection established";
        stopPolling();
      };
      ws.onclose = () => {
        stampEl.textContent = "live connection closed, retrying...";
        startPolling();
        setTimeout(startWebSocket, 1500);
      };
      ws.onerror = () => {
        ws.close();
      };
    }

    fallbackPoll();
    startPolling();
    startWebSocket();
  </script>
</body>
</html>
`
