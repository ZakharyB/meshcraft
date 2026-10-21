package gateway

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/ZakharyB/meshcraft/internal/hosting"
)

func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	return ln.Addr().String()
}

func TestGatewayProxiesOnlyWithValidLease(t *testing.T) {
	target := echoServer(t)
	issuer := hosting.NewAuthority(nil) // the coordinator's authority
	auth := hosting.NewAuthority(nil)   // the gateway's own view
	gw := New(Config{MaxConns: 10, PerIPPerMinute: 100}, auth)

	// No lease observed: registration must fail.
	if err := gw.RegisterRoute("w1", target, "peerA", 1); err == nil {
		t.Fatal("route registered without a valid lease")
	}
	lease, err := issuer.Acquire("w1", "peerA", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !gw.ObserveLease(lease) {
		t.Fatal("fresh lease not observed")
	}
	// A stale/lower token cannot take the route.
	if err := gw.RegisterRoute("w1", target, "peerB", 99); err == nil {
		t.Fatal("route registered for a non-holder")
	}
	if err := gw.RegisterRoute("w1", target, "peerA", lease.Token); err != nil {
		t.Fatalf("valid route rejected: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go gw.Serve(ln)

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte("hello minecraft")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 15)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("no echo through gateway: %v", err)
	}
	if string(buf) != "hello minecraft" {
		t.Fatalf("echo mismatch: %q", buf)
	}
}

func TestGatewayRateLimit(t *testing.T) {
	target := echoServer(t)
	auth := hosting.NewAuthority(nil)
	gw := New(Config{MaxConns: 10, PerIPPerMinute: 1, IdleTimeout: time.Second}, auth)
	issuer := hosting.NewAuthority(nil)
	lease, _ := issuer.Acquire("w1", "peerA", time.Minute)
	gw.ObserveLease(lease)
	if err := gw.RegisterRoute("w1", target, "peerA", lease.Token); err != nil {
		t.Fatal(err)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	go gw.Serve(ln)

	dial := func() bool {
		c, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			return false
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(1500 * time.Millisecond))
		_, _ = c.Write([]byte("x"))
		b := make([]byte, 1)
		_, err = io.ReadFull(c, b)
		return err == nil
	}
	if !dial() {
		t.Fatal("first connection should pass")
	}
	if dial() {
		t.Fatal("second rapid connection should be rate limited")
	}
}
