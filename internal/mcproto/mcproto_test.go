package mcproto

import (
	"bytes"
	"testing"
)

func TestVarIntRoundtrip(t *testing.T) {
	for _, v := range []int32{0, 1, 127, 128, 255, 767, 16383, 2097151, 2147483647, -1} {
		var buf bytes.Buffer
		writeVarInt(&buf, v)
		got, err := readVarInt(&buf)
		if err != nil || got != v {
			t.Fatalf("varint %d -> %d, %v", v, got, err)
		}
	}
	// Known wire encoding: 767 = 0xFF 0x05
	var buf bytes.Buffer
	writeVarInt(&buf, 767)
	if !bytes.Equal(buf.Bytes(), []byte{0xFF, 0x05}) {
		t.Fatalf("767 encoded as % x", buf.Bytes())
	}
}

func TestPacketFraming(t *testing.T) {
	raw := packet(0x00, []byte("abc"))
	id, payload, err := readPacket(bytes.NewReader(raw))
	if err != nil || id != 0 || string(payload) != "abc" {
		t.Fatalf("id=%d payload=%q err=%v", id, payload, err)
	}
}
