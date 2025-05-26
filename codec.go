package cachefunk

import (
	"encoding/json"
	"fmt"

	"github.com/vmihailenco/msgpack/v5"
)

type BodyCodec interface {
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
	String() string
}

type jsonCodec struct{}

var JSONCodec = &jsonCodec{}

func (c *jsonCodec) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (c *jsonCodec) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func (c *jsonCodec) String() string {
	return "json"
}

type msgpackCodec struct{}

var MsgPackCodec = &msgpackCodec{}

func (c *msgpackCodec) Marshal(v any) ([]byte, error) {
	return msgpack.Marshal(v)
}

func (c *msgpackCodec) Unmarshal(data []byte, v any) error {
	return msgpack.Unmarshal(data, v)
}

func (c *msgpackCodec) String() string {
	return "msgpack"
}

type stringCodec struct{}

var StringCodec = &stringCodec{}

func (c *stringCodec) Marshal(v any) ([]byte, error) {
	switch val := v.(type) {
	case string:
		return []byte(val), nil
	case []byte:
		return val, nil
	default:
		return nil, fmt.Errorf("StringCodec.Marshal: unsupported type %T: expected string or []byte", v)
	}
}

func (c *stringCodec) Unmarshal(data []byte, v any) error {
	switch ptr := v.(type) {
	case *string:
		*ptr = string(data)
		return nil
	case *[]byte:
		*ptr = append((*ptr)[:0], data...) // make a copy
		return nil
	default:
		return fmt.Errorf("StringCodec.Unmarshal: unsupported target type %T: expected *string or *[]byte", v)
	}
}

func (c *stringCodec) String() string {
	return "string"
}

var codecMap = map[string]BodyCodec{
	StringCodec.String():  StringCodec,
	JSONCodec.String():    JSONCodec,
	MsgPackCodec.String(): MsgPackCodec,
}
