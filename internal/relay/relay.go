// Package relay implements the encrypted relay fallback (spec §3.1).
//
// Two agents that cannot connect directly both dial OUT to a relay
// and pair with a session code derived from a shared secret. They
// exchange ephemeral X25519 public keys through the relay, derive a
// shared AES-256-GCM key, and from that point the relay is a dumb
// pipe: it forwards ciphertext frames it cannot read. This is the
// DERP-style fallback, with end-to-end encryption done by the agents,
// never by hand-rolled transport crypto on the relay itself.
package relay

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// SessionCode derives the pairing code for a shared secret. Only
// peers who know the secret can pair; the relay learns nothing else.
func SessionCode(secret string) string {
	h := sha256.Sum256([]byte("meshcraft-relay-session:" + secret))
	return hex.EncodeToString(h[:8])
}

type hello struct {
	Session string `json:"session"`
	Role    string `json:"role"` // "host" | "guest"
	PubKey  string `json:"pubkey"`
}

// --- Relay server (dumb pipe) ---

// Server pairs one host and one guest per session code.
type Server struct {
	mu          sync.Mutex
	waiting     map[string]*pendingSide
	PairTimeout time.Duration // how long a lone side may wait (default 60s)
}

type pendingSide struct {
	conn net.Conn
	pub  string
}

// NewServer creates a relay server.
func NewServer() *Server {
	return &Server{waiting: map[string]*pendingSide{}, PairTimeout: 60 * time.Second}
}

// Serve accepts relay clients until the listener closes.
func (s *Server) Serve(ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handle(conn)
	}
}

// ListenAndServe is Serve on addr.
func (s *Server) ListenAndServe(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(ln)
}

// bufferedConn replays buffered bytes before reading the socket.
type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufferedConn) Read(p []byte) (int, error) { return b.r.Read(p) }

func (s *Server) handle(conn net.Conn) {
	br := bufio.NewReader(conn)
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	line, err := br.ReadBytes('\n')
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		conn.Close()
		return
	}
	var h hello
	if err := json.Unmarshal(line, &h); err != nil || h.Session == "" || (h.Role != "host" && h.Role != "guest") || h.PubKey == "" {
		conn.Close()
		return
	}
	wrapped := &bufferedConn{Conn: conn, r: br}

	s.mu.Lock()
	other, exists := s.waiting[h.Session]
	if !exists {
		s.waiting[h.Session] = &pendingSide{conn: wrapped, pub: h.PubKey}
		s.mu.Unlock()
		// Expire the pairing if nobody shows up.
		time.AfterFunc(s.PairTimeout, func() {
			s.mu.Lock()
			if cur, ok := s.waiting[h.Session]; ok && cur.conn == wrapped {
				delete(s.waiting, h.Session)
				_ = wrapped.Close()
			}
			s.mu.Unlock()
		})
		return
	}
	delete(s.waiting, h.Session)
	s.mu.Unlock()

	// Exchange public keys, then pipe ciphertext blindly.
	_ = writeLine(other.conn, hello{Session: h.Session, Role: h.Role, PubKey: h.PubKey})
	_ = writeLine(wrapped, hello{Session: h.Session, Role: otherRole(h.Role), PubKey: other.pub})
	pipeBoth(wrapped, other.conn)
}

func otherRole(r string) string {
	if r == "host" {
		return "guest"
	}
	return "host"
}

func writeLine(conn net.Conn, h hello) error {
	b, _ := json.Marshal(h)
	_, err := conn.Write(append(b, '\n'))
	return err
}

// Pipe copies bytes both ways until either side closes.
func Pipe(a, b net.Conn) { pipeBoth(a, b) }

func pipeBoth(a, b net.Conn) {
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		_ = dst.Close()
		_ = src.Close()
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
}

// --- Agent side: encrypted connection ---

// Dial connects to the relay as the given role and completes the
// pairing + key exchange, returning an encrypted net.Conn.
func Dial(relayAddr, session, role string, timeout time.Duration) (net.Conn, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	raw, err := net.DialTimeout("tcp", relayAddr, timeout)
	if err != nil {
		return nil, err
	}
	_ = raw.SetDeadline(time.Now().Add(timeout))
	br := bufio.NewReader(raw)
	myPub := base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes())
	if err := writeLine(raw, hello{Session: session, Role: role, PubKey: myPub}); err != nil {
		raw.Close()
		return nil, err
	}
	line, err := br.ReadBytes('\n')
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("pairing failed (peer never arrived?): %w", err)
	}
	var peerHello hello
	if err := json.Unmarshal(line, &peerHello); err != nil {
		raw.Close()
		return nil, err
	}
	peerBytes, err := base64.StdEncoding.DecodeString(peerHello.PubKey)
	if err != nil {
		raw.Close()
		return nil, err
	}
	peerPub, err := ecdh.X25519().NewPublicKey(peerBytes)
	if err != nil {
		raw.Close()
		return nil, err
	}
	shared, err := priv.ECDH(peerPub)
	if err != nil {
		raw.Close()
		return nil, err
	}
	_ = raw.SetDeadline(time.Time{})
	conn := &bufferedConn{Conn: raw, r: br}
	// Key derivation: SHA-256 over the shared secret bound to both
	// public keys and the protocol label (prevents cross-protocol and
	// unknown-key-share confusion between the two directions' peers).
	h := sha256.New()
	h.Write([]byte("meshcraft-relay-v1"))
	h.Write(shared)
	if role == "host" {
		h.Write(priv.PublicKey().Bytes())
		h.Write(peerBytes)
	} else {
		h.Write(peerBytes)
		h.Write(priv.PublicKey().Bytes())
	}
	key := h.Sum(nil)
	// Direction-separated nonce prefixes: host->guest 0x01, guest->host 0x02.
	var sendDir, recvDir byte
	if role == "host" {
		sendDir, recvDir = 0x01, 0x02
	} else {
		sendDir, recvDir = 0x02, 0x01
	}
	ec, err := newEncryptedConn(conn, key, sendDir, recvDir)
	if err != nil {
		raw.Close()
		return nil, err
	}
	return ec, nil
}

const maxFrame = 64 * 1024

type encryptedConn struct {
	net.Conn
	gcm     cipher.AEAD
	sendDir byte
	recvDir byte
	sendSeq uint64
	recvSeq uint64
	wmu     sync.Mutex
	rmu     sync.Mutex
	rbuf    []byte
}

func newEncryptedConn(c net.Conn, key []byte, sendDir, recvDir byte) (*encryptedConn, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &encryptedConn{Conn: c, gcm: gcm, sendDir: sendDir, recvDir: recvDir}, nil
}

func (c *encryptedConn) nonce(dir byte, seq uint64) []byte {
	n := make([]byte, 12)
	n[0] = dir
	binary.BigEndian.PutUint64(n[4:], seq)
	return n
}

func (c *encryptedConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	total := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > maxFrame {
			chunk = chunk[:maxFrame]
		}
		nonce := c.nonce(c.sendDir, c.sendSeq)
		c.sendSeq++
		sealed := c.gcm.Seal(nil, nonce, chunk, nil)
		var hdr [4]byte
		binary.BigEndian.PutUint32(hdr[:], uint32(len(sealed)))
		if _, err := c.Conn.Write(hdr[:]); err != nil {
			return total, err
		}
		if _, err := c.Conn.Write(sealed); err != nil {
			return total, err
		}
		total += len(chunk)
		p = p[len(chunk):]
	}
	return total, nil
}

func (c *encryptedConn) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	if len(c.rbuf) == 0 {
		var hdr [4]byte
		if _, err := io.ReadFull(c.Conn, hdr[:]); err != nil {
			return 0, err
		}
		n := binary.BigEndian.Uint32(hdr[:])
		if n > uint32(maxFrame+c.gcm.Overhead()) {
			return 0, fmt.Errorf("relay frame too large: %d", n)
		}
		sealed := make([]byte, n)
		if _, err := io.ReadFull(c.Conn, sealed); err != nil {
			return 0, err
		}
		nonce := c.nonce(c.recvDir, c.recvSeq)
		c.recvSeq++
		plain, err := c.gcm.Open(nil, nonce, sealed, nil)
		if err != nil {
			return 0, fmt.Errorf("relay frame failed authentication (wrong peer or tampered): %w", err)
		}
		c.rbuf = plain
	}
	n := copy(p, c.rbuf)
	c.rbuf = c.rbuf[n:]
	return n, nil
}
