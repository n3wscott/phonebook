package testutil

import "sync"

// TestLogger is a simple threadsafe test logger implementing the Logger interfaces.
type TestLogger struct {
	mu      sync.Mutex
	entries []Entry
}

// Entry represents a logged message.
type Entry struct {
	Level string
	Msg   string
	Args  []any
}

// NewTestLogger creates a logger instance.
func NewTestLogger() *TestLogger {
	return &TestLogger{}
}

// Entries returns a copy of logged entries.
func (l *TestLogger) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	return out
}

func (l *TestLogger) Info(msg string, args ...any) {
	l.append("info", msg, args...)
}

func (l *TestLogger) Warn(msg string, args ...any) {
	l.append("warn", msg, args...)
}

func (l *TestLogger) Debug(msg string, args ...any) {
	l.append("debug", msg, args...)
}

func (l *TestLogger) append(level, msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, Entry{Level: level, Msg: msg, Args: args})
}
