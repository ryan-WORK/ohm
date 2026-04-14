package rpc

import (
	"fmt"
	"io"

	"github.com/ugorji/go/codec"
)

const (
	TypeRequest      = 0
	TypeResponse     = 1
	TypeNotification = 2
)

// Message represents a decoded msgpack-rpc frame.
// Neovim sends either Requests or Notifications.
type Message struct {
	Type   int
	MsgID  uint32
	Method string
	Params []interface{}
}

type Handler struct {
	mh codec.MsgpackHandle
}

func NewHandler() *Handler {
	h := &Handler{}
	h.mh.RawToString = true
	return h
}

// Decode reads one msgpack-rpc frame from r.
// Blocks until a full message arrives or connection closes.
func (h *Handler) Decode(r io.Reader) (*Message, error) {
	var raw []interface{}

	dec := codec.NewDecoder(r, &h.mh)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	if len(raw) < 3 {
		return nil, fmt.Errorf("message too short: %d elements", len(raw))
	}

	msgType, ok := toUint64(raw[0])
	if !ok {
		return nil, fmt.Errorf("invalid message type field: %T", raw[0])
	}

	msg := &Message{Type: int(msgType)}

	switch msg.Type {
	case TypeNotification:
		// [2, method, params]
		msg.Method, _ = raw[1].(string)
		if p, ok := raw[2].([]interface{}); ok {
			msg.Params = p
		}

	case TypeRequest:
		// [0, msgid, method, params]
		if len(raw) < 4 {
			return nil, fmt.Errorf("request too short")
		}
		if id, ok := toUint64(raw[1]); ok {
			msg.MsgID = uint32(id)
		}
		msg.Method, _ = raw[2].(string)
		if p, ok := raw[3].([]interface{}); ok {
			msg.Params = p
		}

	default:
		return nil, fmt.Errorf("unknown message type: %d", msg.Type)
	}

	return msg, nil
}

// toUint64 converts any integer type to uint64.
// ugorji/go/codec decodes small positive integers as int8, not uint64.
func toUint64(v interface{}) (uint64, bool) {
	switch n := v.(type) {
	case uint64:
		return n, true
	case uint32:
		return uint64(n), true
	case uint16:
		return uint64(n), true
	case uint8:
		return uint64(n), true
	case int64:
		if n >= 0 {
			return uint64(n), true
		}
	case int32:
		if n >= 0 {
			return uint64(n), true
		}
	case int16:
		if n >= 0 {
			return uint64(n), true
		}
	case int8:
		if n >= 0 {
			return uint64(n), true
		}
	case int:
		if n >= 0 {
			return uint64(n), true
		}
	}
	return 0, false
}

// WriteResponse sends a msgpack-rpc type 1 (response) frame.
func (h *Handler) WriteResponse(w io.Writer, msgID uint32, result interface{}) error {
	msg := []interface{}{TypeResponse, msgID, nil, result}
	enc := codec.NewEncoder(w, &h.mh)
	return enc.Encode(msg)
}

func (h *Handler) DecodeParam(dst interface{}, src interface{}) error {
	var buf []byte
	enc := codec.NewEncoderBytes(&buf, &h.mh)
	if err := enc.Encode(src); err != nil {
		return err
	}
	dec := codec.NewDecoderBytes(buf, &h.mh)
	return dec.Decode(dst)
}
