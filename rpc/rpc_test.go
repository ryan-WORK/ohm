package rpc

import (
	"bytes"
	"testing"

	"github.com/ugorji/go/codec"
)

func TestWriteResponse_roundtrip(t *testing.T) {
	h := NewHandler()
	var buf bytes.Buffer

	if err := h.WriteResponse(&buf, 42, "proxy-path"); err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}

	var raw []interface{}
	dec := codec.NewDecoder(&buf, &h.mh)
	if err := dec.Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(raw) != 4 {
		t.Fatalf("expected 4 elements, got %d", len(raw))
	}
	typ, _ := toUint64(raw[0])
	if typ != TypeResponse {
		t.Errorf("type: want %d, got %d", TypeResponse, typ)
	}
	msgID, _ := toUint64(raw[1])
	if msgID != 42 {
		t.Errorf("msgid: want 42, got %d", msgID)
	}
	if raw[2] != nil {
		t.Errorf("error field: want nil, got %v", raw[2])
	}
	if raw[3] != "proxy-path" {
		t.Errorf("result: want proxy-path, got %v", raw[3])
	}
}

func TestWriteResponse_nilResult(t *testing.T) {
	h := NewHandler()
	var buf bytes.Buffer
	if err := h.WriteResponse(&buf, 1, nil); err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}
	var raw []interface{}
	dec := codec.NewDecoder(&buf, &h.mh)
	if err := dec.Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if raw[3] != nil {
		t.Errorf("result: want nil, got %v", raw[3])
	}
}

func TestToUint64(t *testing.T) {
	cases := []struct {
		in   interface{}
		want uint64
		ok   bool
	}{
		{uint64(10), 10, true},
		{uint32(10), 10, true},
		{uint16(10), 10, true},
		{uint8(10), 10, true},
		{int64(10), 10, true},
		{int32(10), 10, true},
		{int16(10), 10, true},
		{int8(10), 10, true},
		{int(10), 10, true},
		{int64(-1), 0, false},
		{int8(-1), 0, false},
		{"nope", 0, false},
		{nil, 0, false},
	}
	for _, tc := range cases {
		got, ok := toUint64(tc.in)
		if ok != tc.ok {
			t.Errorf("toUint64(%T(%v)): ok=%v, want %v", tc.in, tc.in, ok, tc.ok)
		}
		if ok && got != tc.want {
			t.Errorf("toUint64(%T(%v)): got %d, want %d", tc.in, tc.in, got, tc.want)
		}
	}
}

func TestDecode_UnknownType(t *testing.T) {
	raw := []interface{}{uint64(9), "method", []interface{}{}}
	r := encodeMsg(t, raw)
	h := NewHandler()
	_, err := h.Decode(r)
	if err == nil {
		t.Fatal("expected error for unknown message type")
	}
}

func TestDecode_TooShort(t *testing.T) {
	raw := []interface{}{uint64(0), uint64(1)} // request missing method+params
	r := encodeMsg(t, raw)
	h := NewHandler()
	_, err := h.Decode(r)
	if err == nil {
		t.Fatal("expected error for too-short request")
	}
}

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
