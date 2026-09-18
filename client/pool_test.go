package client

import (
	"context"
	"testing"
)

func TestPool_RegisterGetCallTool(t *testing.T) {
	pool := NewPool()
	if err := pool.Register("srv", ServerConfig{Transport: "http", URL: "http://example"}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	c, err := pool.Get("srv")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// Swap in a fake dialer directly on the Client this Pool created, since
	// Pool.Register always wires the production dialSDK -- this is an
	// internal test, in the same package, so reaching into the field is the
	// simplest way to exercise Pool's own bookkeeping without a real
	// process or network.
	dialer := newFakeDialer(TransportHTTP)
	c.dial = dialer.dial

	if _, _, err := pool.CallTool(context.Background(), "srv", "tool", nil); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if got := dialer.dialCount(); got != 1 {
		t.Errorf("dial count = %d, want 1", got)
	}
}

func TestPool_GetUnregistered(t *testing.T) {
	pool := NewPool()
	if _, err := pool.Get("missing"); err == nil {
		t.Fatal("Get(missing): want error, got nil")
	}
	if _, _, err := pool.CallTool(context.Background(), "missing", "tool", nil); err == nil {
		t.Fatal("CallTool(missing): want error, got nil")
	}
}

func TestPool_RegisterTwiceClosesOldConnection(t *testing.T) {
	pool := NewPool()
	if err := pool.Register("srv", ServerConfig{Transport: "http", URL: "http://example"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	c, _ := pool.Get("srv")
	sess := &fakeSession{}
	dialer := newFakeDialer(TransportHTTP, sess)
	c.dial = dialer.dial
	if _, _, err := pool.CallTool(context.Background(), "srv", "tool", nil); err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	if err := pool.Register("srv", ServerConfig{Transport: "http", URL: "http://other"}); err != nil {
		t.Fatalf("re-Register: %v", err)
	}
	if !sess.closed {
		t.Error("old connection was not closed on re-Register")
	}

	newC, _ := pool.Get("srv")
	if newC == c {
		t.Error("Get after re-Register returned the same *Client")
	}
}

func TestPool_DeregisterClosesConnection(t *testing.T) {
	pool := NewPool()
	_ = pool.Register("srv", ServerConfig{Transport: "http", URL: "http://example"})
	c, _ := pool.Get("srv")
	sess := &fakeSession{}
	dialer := newFakeDialer(TransportHTTP, sess)
	c.dial = dialer.dial
	if _, _, err := pool.CallTool(context.Background(), "srv", "tool", nil); err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	if err := pool.Deregister("srv"); err != nil {
		t.Fatalf("Deregister: %v", err)
	}
	if !sess.closed {
		t.Error("connection was not closed on Deregister")
	}
	if _, err := pool.Get("srv"); err == nil {
		t.Error("Get after Deregister: want error, got nil")
	}
}

func TestPool_DeregisterUnknownIsNotAnError(t *testing.T) {
	pool := NewPool()
	if err := pool.Deregister("never-registered"); err != nil {
		t.Errorf("Deregister(never-registered) = %v, want nil", err)
	}
}

func TestPool_InvalidateClosesWithoutDeregistering(t *testing.T) {
	pool := NewPool()
	_ = pool.Register("srv", ServerConfig{Transport: "http", URL: "http://example"})
	c, _ := pool.Get("srv")
	sess := &fakeSession{}
	dialer := newFakeDialer(TransportHTTP, sess)
	c.dial = dialer.dial
	if _, _, err := pool.CallTool(context.Background(), "srv", "tool", nil); err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	pool.Invalidate("srv")
	if !sess.closed {
		t.Error("connection was not closed by Invalidate")
	}
	if _, err := pool.Get("srv"); err != nil {
		t.Errorf("Get after Invalidate: %v, want still registered", err)
	}

	// The next call re-dials transparently.
	if _, _, err := pool.CallTool(context.Background(), "srv", "tool", nil); err != nil {
		t.Fatalf("CallTool after Invalidate: %v", err)
	}
	if got := dialer.dialCount(); got != 2 {
		t.Errorf("dial count = %d, want 2 (initial + redial after Invalidate)", got)
	}
}

func TestPool_CloseClosesEveryConnectionAndDeregistersAll(t *testing.T) {
	pool := NewPool()
	_ = pool.Register("a", ServerConfig{Transport: "http", URL: "http://a"})
	_ = pool.Register("b", ServerConfig{Transport: "http", URL: "http://b"})

	ca, _ := pool.Get("a")
	cb, _ := pool.Get("b")
	sessA, sessB := &fakeSession{}, &fakeSession{}
	ca.dial = newFakeDialer(TransportHTTP, sessA).dial
	cb.dial = newFakeDialer(TransportHTTP, sessB).dial
	if _, _, err := pool.CallTool(context.Background(), "a", "tool", nil); err != nil {
		t.Fatalf("CallTool a: %v", err)
	}
	if _, _, err := pool.CallTool(context.Background(), "b", "tool", nil); err != nil {
		t.Fatalf("CallTool b: %v", err)
	}

	if err := pool.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !sessA.closed || !sessB.closed {
		t.Errorf("sessions closed = (%v, %v), want (true, true)", sessA.closed, sessB.closed)
	}
	if _, err := pool.Get("a"); err == nil {
		t.Error("Get(a) after Close: want error, got nil")
	}
}
