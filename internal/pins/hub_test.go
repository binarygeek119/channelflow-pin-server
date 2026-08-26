package pins

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/binarygeek119/channelflow-pin/internal/quickpin"
)

func TestIssueAndDeliver(t *testing.T) {
	h := NewHub()
	s, err := h.Issue()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := quickpin.NormalizePin(s.Pin); err != nil {
		t.Fatalf("issued pin %q: %v", s.Pin, err)
	}
	if quickpin.DisplayPin(s.Pin)[4] != '-' {
		t.Fatalf("display: %s", quickpin.DisplayPin(s.Pin))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got := make(chan string, 1)
	go func() {
		ct, err := h.Wait(ctx, s)
		if err != nil {
			t.Errorf("wait: %v", err)
			close(got)
			return
		}
		got <- ct
	}()
	time.Sleep(20 * time.Millisecond)
	if err := h.Deliver(s.Pin[:4]+"-"+s.Pin[4:], "ciphertext-blob"); err != nil {
		t.Fatal(err)
	}
	select {
	case ct := <-got:
		if ct != "ciphertext-blob" {
			t.Fatalf("got %q", ct)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	if err := h.Deliver(s.Pin, "again"); err != ErrUnknown {
		t.Fatalf("reuse: %v", err)
	}
}

func TestUnknownPin(t *testing.T) {
	h := NewHub()
	if err := h.Deliver("ABCD1234", "x"); err != ErrUnknown {
		t.Fatalf("got %v", err)
	}
}

func TestDisconnectDropsPin(t *testing.T) {
	h := NewHub()
	s, err := h.Issue()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := h.Wait(ctx, s); done <- err }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	if err := h.Deliver(s.Pin, "x"); err != ErrUnknown {
		t.Fatalf("pin still live: %v", err)
	}
}

func TestExpire(t *testing.T) {
	h := NewHub()
	s, err := h.Issue()
	if err != nil {
		t.Fatal(err)
	}
	s.ExpiresAt = time.Now().Add(30 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = h.Wait(ctx, s)
	if err != ErrExpired {
		t.Fatalf("got %v", err)
	}
	if err := h.Deliver(s.Pin, "x"); err != ErrUnknown {
		t.Fatalf("expired pin still deliverable: %v", err)
	}
}

func TestPollRetryKeepsPin(t *testing.T) {
	h := NewHub()
	s, err := h.Issue()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = h.Poll(ctx, s.Pin)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	if err := h.Deliver(s.Pin, "later"); err != nil {
		t.Fatalf("pin dropped after poll timeout: %v", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	ct, err := h.Wait(ctx2, s)
	if err != nil || ct != "later" {
		t.Fatalf("ct=%q err=%v", ct, err)
	}
}

func TestWaitingCount(t *testing.T) {
	h := NewHub()
	s1, _ := h.Issue()
	s2, _ := h.Issue()
	if h.Waiting() != 2 {
		t.Fatalf("waiting=%d", h.Waiting())
	}
	_ = h.Deliver(s1.Pin, "a")
	if h.Waiting() != 2 {
		t.Fatalf("deliver should keep pin until wait consumes it, waiting=%d", h.Waiting())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := h.Wait(ctx, s1); err != nil {
		t.Fatal(err)
	}
	if h.Waiting() != 1 {
		t.Fatalf("waiting=%d", h.Waiting())
	}
	h.Cancel(s2.Pin)
	if h.Waiting() != 0 {
		t.Fatalf("waiting=%d", h.Waiting())
	}
}
