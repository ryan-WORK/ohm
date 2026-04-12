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
	return &Handler{}
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

	msgType, ok := raw[0].(uint64)
	if !ok {
		return nil, fmt.Errorf("invalid message type field")
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
		if id, ok := raw[1].(uint64); ok {
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
