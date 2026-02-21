package calls

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Logger mirrors slog methods used by this package.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Debug(msg string, args ...any)
}

// Options configure the service retention behavior.
type Options struct {
	MaxHistory int
	Retention  time.Duration
}

// AMIConfig configures AMI connection settings.
type AMIConfig struct {
	Addr           string
	Username       string
	Password       string
	ConnectTimeout time.Duration
	ReconnectDelay time.Duration
}

// Call represents an active call.
type Call struct {
	ID      string    `json:"id"`
	From    string    `json:"from"`
	To      string    `json:"to"`
	State   string    `json:"state"`
	Start   time.Time `json:"start"`
	Updated time.Time `json:"updated"`
}

// HistoryCall represents a completed call.
type HistoryCall struct {
	ID          string    `json:"id"`
	From        string    `json:"from"`
	To          string    `json:"to"`
	State       string    `json:"state"`
	EndReason   string    `json:"end_reason"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	DurationSec int64     `json:"duration_sec"`
}

// Presence represents AMI-observed endpoint/contact presence.
type Presence struct {
	ID      string    `json:"id"`
	State   string    `json:"state"`
	Detail  string    `json:"detail,omitempty"`
	Updated time.Time `json:"updated"`
}

// Snapshot is a read model for HTTP/UI clients.
type Snapshot struct {
	Active    []Call        `json:"active"`
	History   []HistoryCall `json:"history"`
	Presences []Presence    `json:"presences"`
	UpdatedAt time.Time     `json:"updated_at"`
}

type activeCall struct {
	Call
	channels map[string]struct{}
}

// Service tracks active and historical calls from AMI.
type Service struct {
	logger Logger
	opts   Options

	mu       sync.RWMutex
	active   map[string]*activeCall
	history  []HistoryCall
	presence map[string]Presence
	updated  time.Time

	subs   map[int]chan struct{}
	nextID int
}

// NewService creates a call service.
func NewService(opts Options, logger Logger) *Service {
	if opts.MaxHistory <= 0 {
		opts.MaxHistory = 100
	}
	if opts.Retention <= 0 {
		opts.Retention = 7 * 24 * time.Hour
	}
	return &Service{
		logger:   logger,
		opts:     opts,
		active:   make(map[string]*activeCall),
		presence: make(map[string]Presence),
		subs:     make(map[int]chan struct{}),
	}
}

// Snapshot returns a copy of active and historical calls.
func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	active := make([]Call, 0, len(s.active))
	for _, call := range s.active {
		active = append(active, call.Call)
	}
	sort.Slice(active, func(i, j int) bool {
		return active[i].Start.After(active[j].Start)
	})

	history := make([]HistoryCall, len(s.history))
	copy(history, s.history)

	presences := make([]Presence, 0, len(s.presence))
	for _, p := range s.presence {
		presences = append(presences, p)
	}
	sort.Slice(presences, func(i, j int) bool {
		if presences[i].State == presences[j].State {
			return presences[i].ID < presences[j].ID
		}
		return presences[i].State < presences[j].State
	})

	return Snapshot{
		Active:    active,
		History:   history,
		Presences: presences,
		UpdatedAt: s.updated,
	}
}

// Subscribe returns a channel that gets signaled on state changes.
func (s *Service) Subscribe() (<-chan struct{}, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.nextID
	s.nextID++
	ch := make(chan struct{}, 1)
	s.subs[id] = ch
	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.subs[id]; ok {
			delete(s.subs, id)
			close(ch)
		}
	}
	return ch, cancel
}

// LoadCDR loads historical calls from CDR CSV, keeping only retention/max limits.
func (s *Service) LoadCDR(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1

	cutoff := time.Now().Add(-s.opts.Retention)
	var loaded []HistoryCall
	for {
		row, err := reader.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return 0, err
		}
		if len(row) < 17 {
			continue
		}
		start, err := parseCDRTime(row[9])
		if err != nil {
			continue
		}
		end, err := parseCDRTime(row[11])
		if err != nil {
			continue
		}
		if end.Before(cutoff) {
			continue
		}
		duration, _ := strconv.ParseInt(strings.TrimSpace(row[12]), 10, 64)
		loaded = append(loaded, HistoryCall{
			ID:          strings.TrimSpace(row[16]),
			From:        strings.TrimSpace(row[1]),
			To:          strings.TrimSpace(row[2]),
			State:       strings.TrimSpace(row[14]),
			EndReason:   strings.TrimSpace(row[14]),
			Start:       start.UTC(),
			End:         end.UTC(),
			DurationSec: duration,
		})
	}

	sort.Slice(loaded, func(i, j int) bool {
		return loaded[i].End.After(loaded[j].End)
	})

	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = loaded
	s.pruneLocked(time.Now())
	s.updated = time.Now().UTC()
	return len(s.history), nil
}

func parseCDRTime(raw string) (time.Time, error) {
	const layout = "2006-01-02 15:04:05"
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, errors.New("empty CDR timestamp")
	}

	localTS, localErr := time.ParseInLocation(layout, value, time.Local)
	utcTS, utcErr := time.ParseInLocation(layout, value, time.UTC)

	switch {
	case localErr == nil && utcErr == nil:
		// Some systems log CDR in local time, others in UTC (usegmtime=yes).
		// Prefer whichever interpretation is closer to "now" to avoid obviously shifted times.
		now := time.Now()
		localDelta := absDuration(now.Sub(localTS))
		utcDelta := absDuration(now.Sub(utcTS))
		if utcDelta < localDelta {
			return utcTS, nil
		}
		return localTS, nil
	case localErr == nil:
		return localTS, nil
	case utcErr == nil:
		return utcTS, nil
	default:
		return time.Time{}, localErr
	}
}

// RunAMI connects to AMI, streams events, and reconnects until ctx cancellation.
func (s *Service) RunAMI(ctx context.Context, cfg AMIConfig) error {
	if cfg.Addr == "" {
		return errors.New("AMI address is required")
	}
	if cfg.Username == "" || cfg.Password == "" {
		return errors.New("AMI username and password are required")
	}
	if cfg.ConnectTimeout <= 0 {
		cfg.ConnectTimeout = 5 * time.Second
	}
	if cfg.ReconnectDelay <= 0 {
		cfg.ReconnectDelay = 5 * time.Second
	}

	for {
		err := s.runAMIConnection(ctx, cfg)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			s.logger.Warn("AMI connection closed", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(cfg.ReconnectDelay):
		}
	}
}

func (s *Service) runAMIConnection(ctx context.Context, cfg AMIConfig) error {
	dialer := net.Dialer{Timeout: cfg.ConnectTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", cfg.Addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	if _, err := reader.ReadString('\n'); err != nil {
		return err
	}
	if err := writeAMILogin(conn, cfg); err != nil {
		return err
	}
	if err := waitAMILogin(reader); err != nil {
		return err
	}
	if err := writeAMIPJSIPShowEndpoints(conn); err != nil {
		return err
	}
	s.logger.Info("AMI connected", "addr", cfg.Addr)

	closeConn := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-closeConn:
		}
	}()
	defer close(closeConn)

	for {
		msg, err := readAMIMessage(reader)
		if err != nil {
			return err
		}
		if msg["Event"] != "" {
			s.HandleAMIEvent(msg)
		}
	}
}

func writeAMILogin(conn net.Conn, cfg AMIConfig) error {
	login := fmt.Sprintf(
		"Action: Login\r\nUsername: %s\r\nSecret: %s\r\nEvents: on\r\n\r\n",
		cfg.Username,
		cfg.Password,
	)
	_, err := io.WriteString(conn, login)
	return err
}

func writeAMIPJSIPShowEndpoints(conn net.Conn) error {
	_, err := io.WriteString(conn, "Action: PJSIPShowEndpoints\r\n\r\n")
	return err
}

func waitAMILogin(reader *bufio.Reader) error {
	for {
		msg, err := readAMIMessage(reader)
		if err != nil {
			return err
		}
		if resp := msg["Response"]; resp != "" {
			if strings.EqualFold(resp, "Success") {
				return nil
			}
			return fmt.Errorf("AMI login failed: %s", msg["Message"])
		}
	}
}

func readAMIMessage(reader *bufio.Reader) (map[string]string, error) {
	msg := make(map[string]string)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if len(msg) == 0 {
				continue
			}
			return msg, nil
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		msg[key] = val
	}
}

// HandleAMIEvent updates active/history state from one AMI event.
func (s *Service) HandleAMIEvent(event map[string]string) {
	now := time.Now().UTC()
	eventType := strings.ToLower(strings.TrimSpace(eventValue(event, "Event")))
	linkedID := linkedIDFor(event)
	if linkedID == "" && !isPresenceEvent(eventType) {
		return
	}

	s.mu.Lock()
	changed := false
	var call *activeCall
	if linkedID != "" {
		call = s.active[linkedID]
	}
	ensureCall := func() {
		if linkedID == "" {
			return
		}
		if call == nil {
			call = s.getOrCreateCallLocked(linkedID, now)
		}
	}

	switch eventType {
	case "newchannel":
		ensureCall()
		if channel := channelKey(event); channel != "" {
			call.channels[channel] = struct{}{}
			changed = true
		}
		if from := firstNonEmpty(
			cleanNumber(eventValue(event, "CallerIDNum", "CallerIDnum")),
			cleanNumber(channelPeer(eventValue(event, "Channel"))),
		); call.From == "" && from != "" {
			call.From = from
			changed = true
		}
		if to := firstNonEmpty(
			cleanTarget(eventValue(event, "Exten")),
			cleanNumber(eventValue(event, "ConnectedLineNum", "ConnectedLineNum")),
		); call.To == "" && to != "" {
			call.To = to
			changed = true
		}
		if state := strings.ToLower(strings.TrimSpace(eventValue(event, "ChannelStateDesc", "Channelstatedesc"))); state != "" {
			call.State = state
			changed = true
		}
	case "dialbegin":
		ensureCall()
		if from := firstNonEmpty(
			cleanNumber(eventValue(event, "CallerIDNum", "CallerIDnum")),
			cleanNumber(channelPeer(eventValue(event, "SrcChannel", "SourceChannel"))),
		); call.From == "" && from != "" {
			call.From = from
			changed = true
		}
		if to := firstNonEmpty(
			cleanNumber(parseDialString(eventValue(event, "DialString", "Dialstring"))),
			cleanNumber(channelPeer(eventValue(event, "DestChannel", "DestinationChannel"))),
		); call.To == "" && to != "" {
			call.To = to
			changed = true
		}
		if src := strings.TrimSpace(eventValue(event, "SrcUniqueID", "SrcUniqueId", "SrcUniqueid")); src != "" {
			call.channels[src] = struct{}{}
			changed = true
		}
		if dst := strings.TrimSpace(eventValue(event, "DestUniqueID", "DestUniqueId", "DestUniqueid")); dst != "" {
			call.channels[dst] = struct{}{}
			changed = true
		}
		call.State = "dialing"
		changed = true
	case "dial":
		subEvent := strings.ToLower(strings.TrimSpace(eventValue(event, "SubEvent", "Subevent")))
		if subEvent == "begin" {
			ensureCall()
			if from := firstNonEmpty(
				cleanNumber(eventValue(event, "CallerIDNum", "CallerIDnum")),
				cleanNumber(channelPeer(eventValue(event, "SrcChannel", "SourceChannel"))),
			); call.From == "" && from != "" {
				call.From = from
				changed = true
			}
			if to := firstNonEmpty(
				cleanNumber(parseDialString(eventValue(event, "DialString", "Dialstring"))),
				cleanNumber(channelPeer(eventValue(event, "DestChannel", "DestinationChannel"))),
			); call.To == "" && to != "" {
				call.To = to
				changed = true
			}
			if src := strings.TrimSpace(eventValue(event, "SrcUniqueID", "SrcUniqueId", "SrcUniqueid")); src != "" {
				call.channels[src] = struct{}{}
				changed = true
			}
			if dst := strings.TrimSpace(eventValue(event, "DestUniqueID", "DestUniqueId", "DestUniqueid")); dst != "" {
				call.channels[dst] = struct{}{}
				changed = true
			}
			call.State = "dialing"
			changed = true
		}
	case "newstate":
		ensureCall()
		if state := strings.ToLower(strings.TrimSpace(eventValue(event, "ChannelStateDesc", "Channelstatedesc"))); state != "" {
			call.State = state
			changed = true
		}
	case "bridgeenter":
		ensureCall()
		call.State = "active"
		if channel := channelKey(event); channel != "" {
			call.channels[channel] = struct{}{}
		}
		changed = true
	case "bridgeleave":
		if call == nil {
			break
		}
		call.State = "ringing"
		changed = true
	case "hangup":
		if call == nil {
			break
		}
		channel := channelKey(event)
		if channel != "" {
			delete(call.channels, channel)
			changed = true
		}
		if len(call.channels) == 0 {
			endReason := strings.TrimSpace(firstNonEmpty(eventValue(event, "Cause-txt"), eventValue(event, "Cause"), eventValue(event, "DialStatus")))
			state := "completed"
			if reason := strings.ToLower(endReason); reason != "" {
				switch {
				case strings.Contains(reason, "404"), strings.Contains(reason, "not found"), strings.Contains(reason, "error"), strings.Contains(reason, "failed"), strings.Contains(reason, "congestion"), strings.Contains(reason, "unavailable"), strings.Contains(reason, "reject"):
					state = "error"
				case strings.Contains(reason, "no answer"), strings.Contains(reason, "busy"), strings.Contains(reason, "cancel"), strings.Contains(reason, "timeout"):
					state = "no-answer"
				case strings.Contains(reason, "normal clearing"), strings.Contains(reason, "answered"):
					state = "answered"
				}
			}
			h := HistoryCall{
				ID:          call.ID,
				From:        call.From,
				To:          call.To,
				State:       state,
				EndReason:   endReason,
				Start:       call.Start.UTC(),
				End:         now,
				DurationSec: int64(now.Sub(call.Start).Seconds()),
			}
			s.history = append([]HistoryCall{h}, s.history...)
			delete(s.active, call.ID)
			changed = true
		}
	case "contactstatus", "endpointstatus", "devicestatechange", "peerstatus", "endpointlist":
		if id, ok := presenceIDFor(event); ok {
			state, detail := presenceStateFor(eventType, event)
			prev, hasPrev := s.presence[id]
			next := Presence{
				ID:      id,
				State:   state,
				Detail:  detail,
				Updated: now,
			}
			if !hasPrev || prev.State != next.State || prev.Detail != next.Detail {
				s.presence[id] = next
				changed = true
			}
		}
	}

	if changed {
		if call != nil {
			call.Updated = now
		}
		s.pruneLocked(now)
		s.updated = now
	}

	subs := s.copySubsLocked()
	s.mu.Unlock()

	if changed {
		notify(subs)
	}
}

func (s *Service) getOrCreateCallLocked(id string, now time.Time) *activeCall {
	if existing, ok := s.active[id]; ok {
		return existing
	}
	call := &activeCall{
		Call: Call{
			ID:      id,
			State:   "ringing",
			Start:   now,
			Updated: now,
		},
		channels: make(map[string]struct{}),
	}
	s.active[id] = call
	return call
}

func (s *Service) pruneLocked(now time.Time) {
	cutoff := now.Add(-s.opts.Retention)
	kept := s.history[:0]
	for _, item := range s.history {
		if item.End.Before(cutoff) {
			continue
		}
		kept = append(kept, item)
		if len(kept) >= s.opts.MaxHistory {
			break
		}
	}
	s.history = kept
}

func (s *Service) copySubsLocked() []chan struct{} {
	out := make([]chan struct{}, 0, len(s.subs))
	for _, ch := range s.subs {
		out = append(out, ch)
	}
	return out
}

func notify(channels []chan struct{}) {
	for _, ch := range channels {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func linkedIDFor(event map[string]string) string {
	id := strings.TrimSpace(firstNonEmpty(
		eventValue(event, "Linkedid", "LinkedID", "LinkedId"),
		eventValue(event, "DestLinkedid", "DestLinkedID", "DestLinkedId"),
		eventValue(event, "SrcLinkedid", "SrcLinkedID", "SrcLinkedId"),
		eventValue(event, "Uniqueid", "UniqueID", "UniqueId"),
		eventValue(event, "DestUniqueid", "DestUniqueID", "DestUniqueId"),
		eventValue(event, "SrcUniqueid", "SrcUniqueID", "SrcUniqueId"),
	))
	if id != "" {
		return id
	}
	channel := strings.TrimSpace(eventValue(event, "Channel", "DestChannel", "SrcChannel"))
	if channel == "" {
		return ""
	}
	// Final fallback for AMI events that do not carry linked/unique IDs.
	return channel
}

func channelKey(event map[string]string) string {
	return strings.TrimSpace(firstNonEmpty(
		eventValue(event, "Uniqueid", "UniqueID", "UniqueId"),
		eventValue(event, "Channel"),
	))
}

func presenceIDFor(event map[string]string) (string, bool) {
	candidates := []string{
		eventValue(event, "EndpointName", "Endpoint", "AOR", "ObjectName", "Peer"),
		eventValue(event, "URI", "Contact"),
		eventValue(event, "Channel", "DestChannel", "SrcChannel"),
		eventValue(event, "Device"),
	}
	for _, candidate := range candidates {
		id := cleanPresenceID(candidate)
		if id != "" {
			return id, true
		}
	}
	return "", false
}

func cleanPresenceID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "<") && strings.Contains(raw, ">") {
		raw = raw[strings.Index(raw, "<")+1 : strings.LastIndex(raw, ">")]
	}
	if strings.Contains(raw, ":") && strings.Contains(raw, "@") {
		if at := strings.Index(raw, "@"); at > 0 {
			colon := strings.LastIndex(raw[:at], ":")
			if colon >= 0 {
				raw = raw[colon+1 : at]
			}
		}
	}
	if strings.Contains(raw, "/") {
		raw = strings.SplitN(raw, "/", 2)[1]
	}
	raw = strings.SplitN(raw, "-", 2)[0]
	raw = strings.SplitN(raw, "@", 2)[0]
	n := cleanNumber(raw)
	if n != "" {
		return n
	}
	return strings.TrimSpace(raw)
}

func presenceStateFor(eventType string, event map[string]string) (string, string) {
	raw := strings.ToLower(strings.TrimSpace(firstNonEmpty(
		eventValue(event, "DeviceState"),
		eventValue(event, "Status"),
		eventValue(event, "EndpointStatus"),
		eventValue(event, "PeerStatus"),
		eventValue(event, "State"),
	)))
	detail := strings.TrimSpace(firstNonEmpty(
		eventValue(event, "ActiveChannels"),
		eventValue(event, "Cause"),
		eventValue(event, "SubEvent"),
		eventValue(event, "URI"),
		eventValue(event, "ContactStatus"),
	))
	if channelsRaw := strings.TrimSpace(eventValue(event, "ActiveChannels")); channelsRaw != "" {
		if channels, err := strconv.Atoi(channelsRaw); err == nil && channels > 0 {
			return "in-use", detail
		}
	}
	if raw == "" {
		raw = eventType
	}
	normalized := strings.NewReplacer(" ", "", "_", "", "-", "").Replace(raw)
	switch {
	case strings.Contains(normalized, "inuse"), strings.Contains(normalized, "busy"), strings.Contains(normalized, "onhold"), strings.Contains(normalized, "ring"), strings.Contains(normalized, "dial"):
		return "in-use", detail
	case strings.Contains(normalized, "notinuse"), strings.Contains(normalized, "reachable"), strings.Contains(normalized, "online"), strings.Contains(normalized, "registered"), strings.Contains(normalized, "avail"), strings.Contains(normalized, "ok"), strings.Contains(normalized, "ready"):
		return "connected", detail
	case strings.Contains(normalized, "unreachable"), strings.Contains(normalized, "offline"), strings.Contains(normalized, "unavailable"), strings.Contains(normalized, "nonqualified"), strings.Contains(normalized, "unknown"), strings.Contains(normalized, "lagged"), strings.Contains(normalized, "removed"), strings.Contains(normalized, "invalid"), strings.Contains(normalized, "failed"):
		return "disconnected", detail
	default:
		return "disconnected", detail
	}
}

func parseDialString(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	part := strings.Split(raw, ",")[0]
	part = strings.Split(part, "&")[0]
	if strings.Contains(part, "/") {
		segments := strings.Split(part, "/")
		part = segments[len(segments)-1]
	}
	return strings.TrimSpace(part)
}

func channelPeer(channel string) string {
	channel = strings.TrimSpace(channel)
	if channel == "" {
		return ""
	}
	if strings.Contains(channel, "/") {
		channel = strings.SplitN(channel, "/", 2)[1]
	}
	channel = strings.SplitN(channel, "-", 2)[0]
	channel = strings.SplitN(channel, "@", 2)[0]
	return channel
}

func cleanTarget(raw string) string {
	raw = cleanNumber(raw)
	switch raw {
	case "", "s", "h", "i":
		return ""
	default:
		return raw
	}
}

func cleanNumber(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range raw {
		if (r >= '0' && r <= '9') || r == '+' || r == '*' || r == '#' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func eventValue(event map[string]string, keys ...string) string {
	for _, key := range keys {
		if v, ok := event[key]; ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	for _, key := range keys {
		for eventKey, eventVal := range event {
			if strings.EqualFold(eventKey, key) && strings.TrimSpace(eventVal) != "" {
				return strings.TrimSpace(eventVal)
			}
		}
	}
	return ""
}

func isPresenceEvent(eventType string) bool {
	switch strings.ToLower(strings.TrimSpace(eventType)) {
	case "contactstatus", "endpointstatus", "devicestatechange", "peerstatus", "endpointlist":
		return true
	default:
		return false
	}
}
