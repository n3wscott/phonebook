package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/n3wscott/phonebook/internal/calls"
	"github.com/n3wscott/phonebook/internal/model"
)

const defaultBroadcastMaxChars = 900

type broadcastContact struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	State   string     `json:"state"`
	Detail  string     `json:"detail,omitempty"`
	Updated *time.Time `json:"updated,omitempty"`
}

type broadcastContactsPayload struct {
	GeneratedAt time.Time          `json:"generated_at"`
	MaxChars    int                `json:"max_chars"`
	From        string             `json:"from"`
	Contacts    []broadcastContact `json:"contacts"`
}

type broadcastSendRequest struct {
	Recipients []string `json:"recipients"`
	Message    string   `json:"message"`
}

type broadcastSendFailure struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

type broadcastSendResponse struct {
	GeneratedAt time.Time              `json:"generated_at"`
	From        string                 `json:"from"`
	Attempted   int                    `json:"attempted"`
	Sent        []string               `json:"sent"`
	Failed      []broadcastSendFailure `json:"failed"`
}

func (s *Server) handleBroadcastPage(w http.ResponseWriter, _ *http.Request) {
	contactsPath := "/api/broadcast/contacts"
	sendPath := "/api/broadcast/send"
	page := fmt.Sprintf(broadcastHTML, contactsPath, sendPath)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(page))
}

func (s *Server) handleBroadcastContacts(w http.ResponseWriter, _ *http.Request) {
	payload := s.buildBroadcastPayload()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) handleBroadcastSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.broadcast.Sender == nil {
		http.Error(w, "broadcast sender is not configured", http.StatusServiceUnavailable)
		return
	}

	var req broadcastSendRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		http.Error(w, "invalid JSON request", http.StatusBadRequest)
		return
	}
	message := strings.TrimSpace(req.Message)
	if message == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}
	maxChars := s.broadcastMaxChars()
	if utf8.RuneCountInString(message) > maxChars {
		http.Error(w, fmt.Sprintf("message exceeds %d character limit", maxChars), http.StatusBadRequest)
		return
	}

	allowed := s.broadcastAllowedRecipients()
	recipients := dedupeRecipients(req.Recipients, allowed)
	if len(recipients) == 0 {
		http.Error(w, "at least one valid recipient is required", http.StatusBadRequest)
		return
	}

	from := s.broadcastFrom()
	resp := broadcastSendResponse{
		GeneratedAt: time.Now().UTC(),
		From:        from,
		Attempted:   len(recipients),
	}
	for _, id := range recipients {
		err := s.broadcast.Sender.SendMessage(r.Context(), calls.Message{
			Destination: "pjsip:" + id,
			To:          "sip:" + id,
			From:        from,
			Body:        message,
		})
		if err != nil {
			resp.Failed = append(resp.Failed, broadcastSendFailure{ID: id, Error: err.Error()})
			continue
		}
		resp.Sent = append(resp.Sent, id)
	}

	status := http.StatusOK
	if len(resp.Sent) == 0 {
		status = http.StatusBadGateway
	} else if len(resp.Failed) > 0 {
		status = http.StatusMultiStatus
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) buildBroadcastPayload() broadcastContactsPayload {
	return broadcastContactsPayload{
		GeneratedAt: time.Now().UTC(),
		MaxChars:    s.broadcastMaxChars(),
		From:        s.broadcastFrom(),
		Contacts:    s.buildBroadcastContacts(),
	}
}

func (s *Server) buildBroadcastContacts() []broadcastContact {
	snap, _ := s.currentSnapshot()
	presenceByID, activeByID := s.broadcastPresenceMaps()

	contacts := make([]broadcastContact, 0, len(snap.Contacts))
	for _, contact := range snap.Contacts {
		if !broadcastEligibleContact(contact) {
			continue
		}
		id := canonicalParty(contact.Extension)
		if id == "" {
			continue
		}
		name := strings.TrimSpace(contact.FirstName + " " + contact.LastName)
		if name == "" {
			name = id
		}
		state := "disconnected"
		detail := "no endpoint presence yet"
		var updated *time.Time
		_, active := activeByID[id]
		if p, ok := presenceByID[id]; ok {
			state = dashboardContactState(p.State, active)
			detail = p.Detail
			if !p.Updated.IsZero() {
				t := p.Updated
				updated = &t
			}
		} else if active {
			state = "in-call"
			detail = "active call"
		}
		contacts = append(contacts, broadcastContact{
			ID:      id,
			Name:    name,
			State:   state,
			Detail:  detail,
			Updated: updated,
		})
	}

	sort.Slice(contacts, func(i, j int) bool {
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
	return contacts
}

func (s *Server) broadcastPresenceMaps() (map[string]calls.Presence, map[string]struct{}) {
	presenceByID := map[string]calls.Presence{}
	activeByID := map[string]struct{}{}
	if s.calls == nil {
		return presenceByID, activeByID
	}
	callSnapshot := s.calls.Snapshot()
	for _, presence := range callSnapshot.Presences {
		id := canonicalParty(presence.ID)
		if id != "" {
			presenceByID[id] = presence
		}
	}
	for _, call := range callSnapshot.Active {
		if id := canonicalParty(call.From); id != "" {
			activeByID[id] = struct{}{}
		}
		if id := canonicalParty(call.To); id != "" {
			activeByID[id] = struct{}{}
		}
	}
	return presenceByID, activeByID
}

func (s *Server) broadcastAllowedRecipients() map[string]struct{} {
	snap, _ := s.currentSnapshot()
	allowed := make(map[string]struct{}, len(snap.Contacts))
	for _, contact := range snap.Contacts {
		if !broadcastEligibleContact(contact) {
			continue
		}
		if id := canonicalParty(contact.Extension); id != "" {
			allowed[id] = struct{}{}
		}
	}
	return allowed
}

func broadcastEligibleContact(contact model.Contact) bool {
	return !contact.PhonebookOnly && !contact.Hidden && strings.TrimSpace(contact.Extension) != ""
}

func dedupeRecipients(input []string, allowed map[string]struct{}) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(input))
	for _, raw := range input {
		id := canonicalParty(raw)
		if id == "" {
			continue
		}
		if _, ok := allowed[id]; !ok {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func (s *Server) broadcastMaxChars() int {
	if s.broadcast.MaxChars <= 0 {
		return defaultBroadcastMaxChars
	}
	return s.broadcast.MaxChars
}

func (s *Server) broadcastFrom() string {
	from := strings.TrimSpace(s.broadcast.From)
	if from == "" {
		return "Operator <sip:operator@localhost>"
	}
	return from
}

const broadcastHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>PBX Broadcast</title>
  <style>
    :root {
      --bg:#f5f1e8;
      --ink:#172017;
      --muted:#687264;
      --line:#d9d0bf;
      --panel:#fffaf0;
      --accent:#0f6b4f;
      --accent-2:#d96f32;
      --danger:#a33a2a;
      --ok:#1f7a4d;
      --shadow:0 18px 50px rgba(42,35,22,.12);
    }
    * { box-sizing:border-box; }
    body { margin:0; background:radial-gradient(circle at 10%% 0%%,#fff8d8 0,#f5f1e8 32%%,#e9e1d1 100%%); color:var(--ink); font-family:"Avenir Next","Segoe UI",sans-serif; }
    .wrap { max-width:1180px; margin:0 auto; padding:34px 18px 42px; }
    header { display:flex; justify-content:space-between; gap:16px; align-items:flex-end; margin-bottom:22px; }
    h1 { margin:0; font-size:clamp(2.2rem,5vw,4.8rem); letter-spacing:-.08em; line-height:.9; }
    .subtitle { margin:10px 0 0; color:var(--muted); font-size:1rem; }
    .stamp { color:var(--muted); font-size:.85rem; text-transform:uppercase; letter-spacing:.08em; }
    .grid { display:grid; grid-template-columns:minmax(0,1fr) 390px; gap:18px; align-items:start; }
    .panel { background:rgba(255,250,240,.9); border:1px solid var(--line); border-radius:28px; box-shadow:var(--shadow); overflow:hidden; }
    .panel-head { padding:18px 20px; border-bottom:1px solid var(--line); display:flex; justify-content:space-between; gap:12px; align-items:center; }
    .panel h2 { margin:0; font-size:1rem; text-transform:uppercase; letter-spacing:.12em; }
    .toolbar { display:flex; gap:8px; flex-wrap:wrap; }
    button { border:0; border-radius:999px; padding:10px 14px; font-weight:800; cursor:pointer; background:var(--ink); color:white; }
    button.secondary { background:#e9dfcd; color:var(--ink); }
    button.accent { background:var(--accent); color:white; width:100%%; padding:14px 18px; font-size:1rem; }
    button:disabled { opacity:.45; cursor:not-allowed; }
    .contacts { list-style:none; padding:0; margin:0; max-height:68vh; overflow:auto; }
    .contacts li { display:grid; grid-template-columns:auto 1fr auto; gap:12px; align-items:center; padding:14px 18px; border-bottom:1px solid rgba(217,208,191,.75); }
    .contacts li:last-child { border-bottom:0; }
    input[type="checkbox"] { width:20px; height:20px; accent-color:var(--accent); }
    .name { font-weight:850; }
    .meta { color:var(--muted); font-size:.88rem; margin-top:3px; }
    .badge { border-radius:999px; padding:6px 9px; font-size:.75rem; font-weight:900; text-transform:uppercase; letter-spacing:.05em; white-space:nowrap; }
    .connected { background:#dff3df; color:#17613a; }
    .in-call { background:#ffe2b9; color:#8a4b00; }
    .disconnected { background:#ece6db; color:#665b4d; }
    .composer { padding:18px; display:grid; gap:14px; }
    textarea { width:100%%; min-height:180px; resize:vertical; border:1px solid var(--line); border-radius:18px; padding:14px; font:inherit; line-height:1.4; background:#fffef8; color:var(--ink); }
    textarea:focus { outline:3px solid rgba(15,107,79,.18); border-color:var(--accent); }
    .count { display:flex; justify-content:space-between; gap:10px; color:var(--muted); font-size:.9rem; }
    .count.over { color:var(--danger); font-weight:800; }
    .summary { background:#f0e7d8; border-radius:18px; padding:12px 14px; color:var(--muted); font-size:.92rem; }
    .result { white-space:pre-wrap; border-radius:18px; padding:12px 14px; background:#edf7ed; color:var(--ok); display:none; }
    .result.error { background:#fae8e2; color:var(--danger); }
    @media (max-width:840px) { .grid { grid-template-columns:1fr; } header { align-items:flex-start; flex-direction:column; } .contacts { max-height:none; } }
  </style>
</head>
<body>
  <div class="wrap">
    <header>
      <div>
        <h1>Broadcast</h1>
        <p class="subtitle">Send one SIP MESSAGE from the operator identity to selected PBX contacts.</p>
      </div>
      <div id="stamp" class="stamp">loading contacts...</div>
    </header>
    <div class="grid">
      <section class="panel">
        <div class="panel-head">
          <h2>Recipients</h2>
          <div class="toolbar">
            <button class="secondary" id="selectConnected" type="button">Select Connected</button>
            <button class="secondary" id="selectAll" type="button">Select All</button>
            <button class="secondary" id="clearAll" type="button">Clear</button>
          </div>
        </div>
        <ul id="contacts" class="contacts"></ul>
      </section>
      <section class="panel">
        <div class="panel-head"><h2>Message</h2></div>
        <div class="composer">
          <div id="summary" class="summary">No recipients selected.</div>
          <textarea id="message" placeholder="Type a short broadcast message..."></textarea>
          <div id="count" class="count"><span>0 / 900 characters</span><span>From operator</span></div>
          <button id="send" class="accent" type="button" disabled>Send Broadcast</button>
          <div id="result" class="result"></div>
        </div>
      </section>
    </div>
  </div>
  <script>
    const contactsApi = %q;
    const sendApi = %q;
    const contactsEl = document.getElementById("contacts");
    const stampEl = document.getElementById("stamp");
    const messageEl = document.getElementById("message");
    const countEl = document.getElementById("count");
    const sendEl = document.getElementById("send");
    const resultEl = document.getElementById("result");
    const summaryEl = document.getElementById("summary");
    let contacts = [];
    let maxChars = 900;
    let fromLabel = "operator";

    function statusClass(state) {
      const s = String(state || "").toLowerCase();
      if (s === "in-call") return "in-call";
      if (s === "connected") return "connected";
      return "disconnected";
    }

    function renderContacts() {
      contactsEl.innerHTML = "";
      if (contacts.length === 0) {
        const li = document.createElement("li");
        li.textContent = "No broadcastable contacts found.";
        contactsEl.appendChild(li);
        return;
      }
      contacts.forEach((contact) => {
        const li = document.createElement("li");
        const cb = document.createElement("input");
        cb.type = "checkbox";
        cb.value = contact.id;
        cb.addEventListener("change", updateState);
        const body = document.createElement("div");
        const name = document.createElement("div");
        name.className = "name";
        name.textContent = contact.name + " (" + contact.id + ")";
        const meta = document.createElement("div");
        meta.className = "meta";
        meta.textContent = contact.detail || "";
        body.appendChild(name);
        body.appendChild(meta);
        const badge = document.createElement("span");
        badge.className = "badge " + statusClass(contact.state);
        badge.textContent = String(contact.state || "unknown").replace("-", " ");
        li.appendChild(cb);
        li.appendChild(body);
        li.appendChild(badge);
        contactsEl.appendChild(li);
      });
      updateState();
    }

    function selectedIDs() {
      return Array.from(contactsEl.querySelectorAll("input[type=checkbox]:checked")).map((el) => el.value);
    }

    function updateState() {
      const selected = selectedIDs();
      const chars = Array.from(messageEl.value).length;
      const over = chars > maxChars;
      countEl.className = "count" + (over ? " over" : "");
      countEl.firstElementChild.textContent = chars + " / " + maxChars + " characters";
      countEl.lastElementChild.textContent = "From " + fromLabel;
      summaryEl.textContent = selected.length === 0 ? "No recipients selected." : selected.length + " recipient" + (selected.length === 1 ? "" : "s") + " selected.";
      sendEl.disabled = selected.length === 0 || chars === 0 || over;
    }

    async function loadContacts() {
      const res = await fetch(contactsApi);
      if (!res.ok) throw new Error("contacts load failed: " + res.status);
      const payload = await res.json();
      contacts = payload.contacts || [];
      maxChars = payload.max_chars || 900;
      fromLabel = payload.from || "operator";
      stampEl.textContent = "updated " + new Date(payload.generated_at).toLocaleTimeString();
      renderContacts();
    }

    async function sendBroadcast() {
      resultEl.style.display = "none";
      sendEl.disabled = true;
      try {
        const res = await fetch(sendApi, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ recipients: selectedIDs(), message: messageEl.value })
        });
        const text = await res.text();
        let payload = {};
        try { payload = JSON.parse(text); } catch (_) {}
        if (!res.ok && res.status !== 207) throw new Error(text || ("send failed: " + res.status));
        const failed = payload.failed || [];
        const sent = payload.sent || [];
        resultEl.className = "result" + (failed.length ? " error" : "");
        resultEl.textContent = "Sent: " + sent.length + "\nFailed: " + failed.length + (failed.length ? "\n" + failed.map((f) => f.id + ": " + f.error).join("\n") : "");
        resultEl.style.display = "block";
      } catch (err) {
        resultEl.className = "result error";
        resultEl.textContent = err.message || String(err);
        resultEl.style.display = "block";
      } finally {
        updateState();
      }
    }

    document.getElementById("selectAll").addEventListener("click", () => {
      contactsEl.querySelectorAll("input[type=checkbox]").forEach((el) => { el.checked = true; });
      updateState();
    });
    document.getElementById("selectConnected").addEventListener("click", () => {
      contactsEl.querySelectorAll("input[type=checkbox]").forEach((el) => {
        const contact = contacts.find((c) => c.id === el.value);
        el.checked = contact && (contact.state === "connected" || contact.state === "in-call");
      });
      updateState();
    });
    document.getElementById("clearAll").addEventListener("click", () => {
      contactsEl.querySelectorAll("input[type=checkbox]").forEach((el) => { el.checked = false; });
      updateState();
    });
    messageEl.addEventListener("input", updateState);
    sendEl.addEventListener("click", sendBroadcast);
    loadContacts().catch((err) => {
      stampEl.textContent = "contact load failed";
      resultEl.className = "result error";
      resultEl.textContent = err.message || String(err);
      resultEl.style.display = "block";
    });
  </script>
</body>
</html>
`
