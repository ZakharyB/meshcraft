// Package mcproto is a minimal Minecraft Java Edition protocol client
// used to verify servers the same way the real game client does in
// its server browser: handshake -> status request -> ping. It also
// provides a login probe (handshake state=login + Login Start) that
// reports the server's first response packet; completing an encrypted
// online-mode login requires Mojang session auth and is intentionally
// out of scope here — a real client with an account does that part.
package mcproto

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"
)

// ProtocolVersion for Minecraft 1.21.1.
const ProtocolVersion = 767

// Status is the decoded server-list status response.
type Status struct {
	VersionName     string `json:"-"`
	VersionProtocol int    `json:"-"`
	PlayersMax      int    `json:"-"`
	PlayersOnline   int    `json:"-"`
	Description     string `json:"-"`
	Raw             string `json:"-"`
}

type statusJSON struct {
	Version struct {
		Name     string `json:"name"`
		Protocol int    `json:"protocol"`
	} `json:"version"`
	Players struct {
		Max    int `json:"max"`
		Online int `json:"online"`
	} `json:"players"`
	Description json.RawMessage `json:"description"`
}

func writeVarInt(buf *bytes.Buffer, v int32) {
	uv := uint32(v)
	for {
		if uv&^0x7F == 0 {
			buf.WriteByte(byte(uv))
			return
		}
		buf.WriteByte(byte(uv&0x7F | 0x80))
		uv >>= 7
	}
}

func readVarInt(r io.Reader) (int32, error) {
	var result uint32
	var shift uint
	one := make([]byte, 1)
	for {
		if _, err := io.ReadFull(r, one); err != nil {
			return 0, err
		}
		b := one[0]
		result |= uint32(b&0x7F) << shift
		if b&0x80 == 0 {
			return int32(result), nil
		}
		shift += 7
		if shift >= 35 {
			return 0, fmt.Errorf("varint too long")
		}
	}
}

func writeString(buf *bytes.Buffer, s string) {
	writeVarInt(buf, int32(len(s)))
	buf.WriteString(s)
}

// packet frames: VarInt(length) + VarInt(packetID) + payload.
func packet(id int32, payload []byte) []byte {
	var body bytes.Buffer
	writeVarInt(&body, id)
	body.Write(payload)
	var out bytes.Buffer
	writeVarInt(&out, int32(body.Len()))
	out.Write(body.Bytes())
	return out.Bytes()
}

func readPacket(r io.Reader) (int32, []byte, error) {
	length, err := readVarInt(r)
	if err != nil {
		return 0, nil, err
	}
	if length <= 0 || length > 10*1024*1024 {
		return 0, nil, fmt.Errorf("bad packet length %d", length)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return 0, nil, err
	}
	br := bytes.NewReader(data)
	id, err := readVarInt(br)
	if err != nil {
		return 0, nil, err
	}
	rest, _ := io.ReadAll(br)
	return id, rest, nil
}

func handshake(host string, port int, nextState int32) []byte {
	var p bytes.Buffer
	writeVarInt(&p, ProtocolVersion)
	writeString(&p, host)
	_ = binary.Write(&p, binary.BigEndian, uint16(port))
	writeVarInt(&p, nextState)
	return packet(0x00, p.Bytes())
}

func dial(addr string, timeout time.Duration) (net.Conn, *bufio.Reader, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	return conn, bufio.NewReader(conn), nil
}

// PingStatus performs the full server-list exchange a Java client does
// before joining: handshake(state=status), status request/response,
// ping/pong. Returns the decoded status and the pong round-trip.
func PingStatus(addr string, timeout time.Duration) (Status, time.Duration, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return Status{}, 0, err
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		return Status{}, 0, err
	}
	conn, r, err := dial(addr, timeout)
	if err != nil {
		return Status{}, 0, err
	}
	defer conn.Close()

	if _, err := conn.Write(handshake(host, port, 1)); err != nil {
		return Status{}, 0, err
	}
	if _, err := conn.Write(packet(0x00, nil)); err != nil { // Status Request
		return Status{}, 0, err
	}
	id, payload, err := readPacket(r)
	if err != nil {
		return Status{}, 0, fmt.Errorf("status response: %w", err)
	}
	if id != 0x00 {
		return Status{}, 0, fmt.Errorf("unexpected status packet id %#x", id)
	}
	br := bytes.NewReader(payload)
	n, err := readVarInt(br)
	if err != nil {
		return Status{}, 0, err
	}
	jsonBytes := make([]byte, n)
	if _, err := io.ReadFull(br, jsonBytes); err != nil {
		return Status{}, 0, err
	}
	var sj statusJSON
	if err := json.Unmarshal(jsonBytes, &sj); err != nil {
		return Status{}, 0, fmt.Errorf("decode status json: %w", err)
	}
	st := Status{
		VersionName: sj.Version.Name, VersionProtocol: sj.Version.Protocol,
		PlayersMax: sj.Players.Max, PlayersOnline: sj.Players.Online,
		Description: string(sj.Description), Raw: string(jsonBytes),
	}

	// Ping request with a random payload; server must echo it in pong.
	var token int64
	_ = binary.Read(rand.Reader, binary.BigEndian, &token)
	var p bytes.Buffer
	_ = binary.Write(&p, binary.BigEndian, token)
	start := time.Now()
	if _, err := conn.Write(packet(0x01, p.Bytes())); err != nil {
		return st, 0, err
	}
	id, payload, err = readPacket(r)
	if err != nil {
		return st, 0, fmt.Errorf("pong: %w", err)
	}
	if id != 0x01 || len(payload) != 8 || binary.BigEndian.Uint64(payload) != uint64(token) {
		return st, 0, fmt.Errorf("pong mismatch (id=%#x)", id)
	}
	return st, time.Since(start), nil
}

// LoginResult describes the server's first response to a Login Start.
type LoginResult struct {
	// One of: "encryption_request" (online/offline vanilla flow wants
	// encryption next), "login_success", "disconnect", "set_compression".
	Kind string
	// Disconnect reason text when Kind == "disconnect".
	Reason string
}

// LoginProbe sends handshake(state=login) + Login Start with a random
// offline UUID and reports the server's first login-phase packet.
// Reaching this response proves the full client join path up to auth.
func LoginProbe(addr, username string, timeout time.Duration) (LoginResult, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return LoginResult{}, err
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		return LoginResult{}, err
	}
	conn, r, err := dial(addr, timeout)
	if err != nil {
		return LoginResult{}, err
	}
	defer conn.Close()
	if _, err := conn.Write(handshake(host, port, 2)); err != nil {
		return LoginResult{}, err
	}
	var p bytes.Buffer
	writeString(&p, username)
	uuid := make([]byte, 16)
	_, _ = rand.Read(uuid)
	p.Write(uuid)
	if _, err := conn.Write(packet(0x00, p.Bytes())); err != nil {
		return LoginResult{}, err
	}
	id, payload, err := readPacket(r)
	if err != nil {
		return LoginResult{}, fmt.Errorf("login response: %w", err)
	}
	switch id {
	case 0x01:
		return LoginResult{Kind: "encryption_request"}, nil
	case 0x02:
		return LoginResult{Kind: "login_success"}, nil
	case 0x03:
		return LoginResult{Kind: "set_compression"}, nil
	case 0x00:
		br := bytes.NewReader(payload)
		n, _ := readVarInt(br)
		reason := make([]byte, n)
		_, _ = io.ReadFull(br, reason)
		return LoginResult{Kind: "disconnect", Reason: string(reason)}, nil
	default:
		return LoginResult{Kind: fmt.Sprintf("unknown_packet_%#x", id)}, nil
	}
}
