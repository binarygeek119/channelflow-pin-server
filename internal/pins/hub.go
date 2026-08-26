package pins

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/binarygeek119/channelflow-pin/internal/quickpin"
)

const Lifetime = 10 * time.Minute

var (
	ErrUnknown = errors.New("unknown pin")
	ErrExpired = errors.New("expired")
	ErrBusy    = errors.New("pin already used")
)

type Session struct {
	Pin       string
	ExpiresAt time.Time
	payload   chan string
	done      chan struct{}
	once      sync.Once
	waiting   atomic.Bool
}

func (s *Session) close() {
	s.once.Do(func() { close(s.done) })
}

func (s *Session) ExpiresIn() time.Duration {
	d := time.Until(s.ExpiresAt)
	if d < 0 {
		return 0
	}
	return d
}

// Hub holds waiting apps. It stores pins and opaque ciphertext only.
type Hub struct {
	mu       sync.Mutex
	sessions map[string]*Session
	fails    int
	failWin  time.Time
}

func NewHub() *Hub {
	return &Hub{sessions: make(map[string]*Session)}
}

func (h *Hub) Issue() (*Session, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sweepLocked(time.Now())
	for i := 0; i < 32; i++ {
		pin, err := quickpin.RandomPin()
		if err != nil {
			return nil, err
		}
		if _, ok := h.sessions[pin]; ok {
			continue
		}
		s := &Session{
			Pin:       pin,
			ExpiresAt: time.Now().Add(Lifetime),
			payload:   make(chan string, 1),
			done:      make(chan struct{}),
		}
		h.sessions[pin] = s
		return s, nil
	}
	return nil, errors.New("could not allocate pin")
}

func (h *Hub) Deliver(rawPin, ciphertext string) error {
	pin, err := quickpin.NormalizePin(rawPin)
	if err != nil {
		h.noteFail()
		return ErrUnknown
	}
	h.mu.Lock()
	h.sweepLocked(time.Now())
	s, ok := h.sessions[pin]
	if !ok {
		h.mu.Unlock()
		h.noteFail()
		return ErrUnknown
	}
	h.mu.Unlock()
	select {
	case s.payload <- ciphertext:
		return nil
	default:
		return ErrBusy
	}
}

// Wait blocks until ciphertext arrives, the pin expires, or ctx is cancelled
// (app disconnect). The pin is always removed afterwards. Only one Wait per session.
func (h *Hub) Wait(ctx context.Context, s *Session) (string, error) {
	return h.wait(ctx, s, true)
}

// WaitPin looks up a pin issued earlier (long-poll) and waits on it.
func (h *Hub) WaitPin(ctx context.Context, rawPin string) (string, error) {
	s, err := h.lookup(rawPin)
	if err != nil {
		return "", err
	}
	return h.wait(ctx, s, true)
}

// Poll waits without dropping the pin when ctx times out so the app can retry.
// Client disconnect should still cancel the request context's parent; pass
// drop=false only for the inner poll deadline.
func (h *Hub) Poll(ctx context.Context, rawPin string) (string, error) {
	s, err := h.lookup(rawPin)
	if err != nil {
		return "", err
	}
	return h.wait(ctx, s, false)
}

func (h *Hub) lookup(rawPin string) (*Session, error) {
	pin, err := quickpin.NormalizePin(rawPin)
	if err != nil {
		return nil, ErrUnknown
	}
	h.mu.Lock()
	h.sweepLocked(time.Now())
	s, ok := h.sessions[pin]
	h.mu.Unlock()
	if !ok {
		return nil, ErrUnknown
	}
	return s, nil
}

func (h *Hub) wait(ctx context.Context, s *Session, dropOnCancel bool) (string, error) {
	if !s.waiting.CompareAndSwap(false, true) {
		return "", ErrBusy
	}
	timer := time.NewTimer(s.ExpiresIn())
	defer timer.Stop()
	select {
	case ct := <-s.payload:
		h.Cancel(s.Pin)
		return ct, nil
	case <-s.done:
		s.waiting.Store(false)
		if ct, ok := h.takePayload(s); ok {
			return ct, nil
		}
		return "", ErrExpired
	case <-timer.C:
		h.Cancel(s.Pin)
		return "", ErrExpired
	case <-ctx.Done():
		if dropOnCancel {
			h.Cancel(s.Pin)
		} else {
			s.waiting.Store(false)
		}
		return "", ctx.Err()
	}
}

// Cancel drops a waiting pin (disconnect or failed websocket).
func (h *Hub) Cancel(pin string) {
	h.drop(pin)
}

func (h *Hub) takePayload(s *Session) (string, bool) {
	select {
	case ct := <-s.payload:
		return ct, true
	default:
		return "", false
	}
}

func (h *Hub) drop(pin string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.sessions[pin]; ok {
		delete(h.sessions, pin)
		s.close()
	}
}

func (h *Hub) Waiting() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sweepLocked(time.Now())
	return len(h.sessions)
}

func (h *Hub) RateLimited() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if time.Since(h.failWin) > time.Minute {
		h.fails = 0
		h.failWin = time.Now()
	}
	return h.fails > 30
}

func (h *Hub) noteFail() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if time.Since(h.failWin) > time.Minute {
		h.fails = 0
		h.failWin = time.Now()
	}
	h.fails++
}

func (h *Hub) sweepLocked(now time.Time) {
	for pin, s := range h.sessions {
		if now.After(s.ExpiresAt) {
			delete(h.sessions, pin)
			s.close()
		}
	}
}
