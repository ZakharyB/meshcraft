package relay

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func startRelay(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer()
	s.PairTimeout = 2 * time.Second
	go s.Serve(ln)
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String()
}

func TestRelayEndToEndEncryptedEcho(t *testing.T) {
	relayAddr := startRelay(t)
	session := SessionCode("test-shared-secret")

	// Host side: echo whatever arrives over the encrypted channel.
	hostErr := make(chan error, 1)
	go func() {
		conn, err := Dial(relayAddr, session, "host", 5*time.Second)
		if err != nil {
			hostErr <- err
			return
		}
		defer conn.Close()
		_, err = io.Copy(conn, conn) // echo
		hostErr <- err
	}()

	guest, err := Dial(relayAddr, session, "guest", 5*time.Second)
	if err != nil {
		t.Fatalf("guest dial: %v", err)
	}
	defer guest.Close()
	_ = guest.SetDeadline(time.Now().Add(5 * time.Second))

	msg := []byte("hello over the encrypted relay")
	if _, err := guest.Write(msg); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(msg))
	if _, err := io.ReadFull(guest, got); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("echo mismatch: %q", got)
	}

	// A payload larger than one frame must survive framing intact.
	big := bytes.Repeat([]byte("meshcraft!"), 30000) // 300KB
	go guest.Write(big)
	gotBig := make([]byte, len(big))
	if _, err := io.ReadFull(guest, gotBig); err != nil {
		t.Fatalf("read big echo: %v", err)
	}
	if !bytes.Equal(gotBig, big) {
		t.Fatal("big payload corrupted across frames")
	}
}

func TestWrongKeyCannotDecrypt(t *testing.T) {
	a1, b1 := net.Pipe()
	keyA := bytes.Repeat([]byte{0xAA}, 32)
	keyB := bytes.Repeat([]byte{0xBB}, 32)
	sender, err := newEncryptedConn(a1, keyA, 0x01, 0x02)
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := newEncryptedConn(b1, keyB, 0x02, 0x01)
	if err != nil {
		t.Fatal(err)
	}
	go sender.Write([]byte("secret world data"))
	buf := make([]byte, 64)
	if _, err := receiver.Read(buf); err == nil {
		t.Fatal("receiver with the wrong key decrypted the frame")
	}
}

func TestUnpairedSessionExpires(t *testing.T) {
	relayAddr := startRelay(t)
	start := time.Now()
	_, err := Dial(relayAddr, SessionCode("nobody-else"), "host", 1500*time.Millisecond)
	if err == nil {
		t.Fatal("unpaired host dial succeeded")
	}
	if time.Since(start) > 4*time.Second {
		t.Fatal("pairing timeout took too long")
	}
}

func TestSessionCodeSecrecyShape(t *testing.T) {
	a := SessionCode("secret-one")
	b := SessionCode("secret-two")
	if a == b || len(a) != 16 {
		t.Fatalf("session codes wrong: %q %q", a, b)
	}
	if a == "secret-one" {
		t.Fatal("session code leaks the secret")
	}
}
