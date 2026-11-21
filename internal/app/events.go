package app

import (
	"sync"
	"sync/atomic"
	"time"
)

// Event is one entry in the in-memory activity feed shown in the UI.
type Event struct {
	At        int64  `json:"at"`        // unix milliseconds
	Direction string `json:"direction"` // in, out, sys
	Kind      string `json:"kind"`      // text, image, files, conn, error, config
	Message   string `json:"message"`
}

type eventLog struct {
	mu    sync.Mutex
	items []Event
	max   int
}

func newEventLog(max int) *eventLog {
	return &eventLog{max: max}
}

func (l *eventLog) Add(direction, kind, message string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append(l.items, Event{
		At:        time.Now().UnixMilli(),
		Direction: direction,
		Kind:      kind,
		Message:   message,
	})
	if len(l.items) > l.max {
		l.items = append([]Event(nil), l.items[len(l.items)-l.max:]...)
	}
}

// Snapshot returns newest first.
func (l *eventLog) Snapshot() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, len(l.items))
	for i := range l.items {
		out[i] = l.items[len(l.items)-1-i]
	}
	return out
}

type counters struct {
	sent     atomic.Int64
	received atomic.Int64
	bytesOut atomic.Int64
	bytesIn  atomic.Int64
}

type transferView struct {
	Active    bool   `json:"active"`
	Direction string `json:"direction"`
	Peer      string `json:"peer"`
	Files     int    `json:"files"`
	Done      int64  `json:"done"`
	Total     int64  `json:"total"`
	Error     string `json:"error"`
}

type transferTracker struct {
	mu sync.Mutex
	v  transferView
}

func (t *transferTracker) start(direction, peer string, files int, total int64) {
	t.mu.Lock()
	t.v = transferView{Active: true, Direction: direction, Peer: peer, Files: files, Total: total}
	t.mu.Unlock()
}

func (t *transferTracker) add(n int64) {
	t.mu.Lock()
	t.v.Done += n
	t.mu.Unlock()
}

func (t *transferTracker) finish(errMsg string) {
	t.mu.Lock()
	t.v.Active = false
	t.v.Error = errMsg
	t.mu.Unlock()
}

func (t *transferTracker) snapshot() transferView {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.v
}
