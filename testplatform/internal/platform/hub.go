package platform

import (
	"encoding/json"
	"sync"
	"time"
)

// HubMessage is the envelope broadcast to all SSE subscribers.
type HubMessage struct {
	Type string    `json:"type"`
	Time time.Time `json:"time"`
	Data any       `json:"data"`
}

// Hub is a fan-out broadcaster feeding the web UI via Server-Sent Events.
type Hub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

// NewHub creates an empty hub.
func NewHub() *Hub {
	return &Hub{subs: make(map[chan []byte]struct{})}
}

// Subscribe registers a new subscriber channel. Call the returned cancel
// function to unsubscribe.
func (h *Hub) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 512)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	cancel := func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
	return ch, cancel
}

// Publish broadcasts a typed message to all subscribers. Slow subscribers
// have messages dropped rather than blocking the producer.
func (h *Hub) Publish(msgType string, data any) {
	payload, err := json.Marshal(HubMessage{Type: msgType, Time: time.Now(), Data: data})
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- payload:
		default: // Drop for slow consumers; the UI refetches state on demand.
		}
	}
}

// SubscriberCount returns the number of active subscribers.
func (h *Hub) SubscriberCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}
