package rpc

import (
	"bytes"
	"testing"

	"github.com/ugorji/go/codec"
)

func encodeMsg(t *testing.T, v interface{}) *bytes.Reader {
	t.Helper()
	var mh codec.MsgpackHandle
	var buf []byte
	enc := codec.NewEncoderBytes(&buf, &mh)
	if err := enc.Encode(v); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return bytes.NewReader(buf)
}

func TestDecode_Notification(t *testing.T) {
	// [2, "attach", [{"root_dir": "/tmp"}]]
	raw := []interface{}{uint64(2), "attach", []interface{}{map[string]interface{}{"root_dir": "/tmp"}}}
	r := encodeMsg(t, raw)

	h := NewHandler()
	msg, err := h.Decode(r)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if msg.Type != TypeNotification {
		t.Errorf("Type: want %d, got %d", TypeNotification, msg.Type)
	}
	if msg.Method != "attach" {
		t.Errorf("Method: want attach, got %s", msg.Method)
	}
	if len(msg.Params) != 1 {
		t.Errorf("Params len: want 1, got %d", len(msg.Params))
	}
}

func TestDecode_Request(t *testing.T) {
	// [0, 42, "ping", []]
	raw := []interface{}{uint64(0), uint64(42), "ping", []interface{}{}}
	r := encodeMsg(t, raw)

	h := NewHandler()
	msg, err := h.Decode(r)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if msg.Type != TypeRequest {
		t.Errorf("Type: want %d, got %d", TypeRequest, msg.Type)
	}
	if msg.MsgID != 42 {
		t.Errorf("MsgID: want 42, got %d", msg.MsgID)
	}
	if msg.Method != "ping" {
		t.Errorf("Method: want ping, got %s", msg.Method)
	}
}

func TestDecodeParam(t *testing.T) {
	type Params struct {
		RootDir    string `codec:"root_dir"`
		LanguageID string `codec:"language_id"`
	}

	// simulate what Decode produces for params[0]
	raw := []interface{}{uint64(2), "attach", []interface{}{
		map[string]interface{}{"root_dir": "/srv/proj", "language_id": "go"},
	}}
	r := encodeMsg(t, raw)

	h := NewHandler()
	msg, err := h.Decode(r)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	var p Params
	if err := h.DecodeParam(&p, msg.Params[0]); err != nil {
		t.Fatalf("DecodeParam: %v", err)
	}
	if p.RootDir != "/srv/proj" {
		t.Errorf("RootDir: want /srv/proj, got %s", p.RootDir)
	}
	if p.LanguageID != "go" {
		t.Errorf("LanguageID: want go, got %s", p.LanguageID)
	}
}
