package cachefunk

import (
	"encoding/json"
	"fmt"
	"slices"
)

// BodyCodec encodes and decodes cached values. A [KeyConfig] selects a body
// codec by String.
type BodyCodec interface {
	// Marshal encodes a value for storage.
	Marshal(v any) ([]byte, error)
	// Unmarshal decodes data into v, which must be a pointer.
	Unmarshal(data []byte, v any) error
	// String is the body codec's configuration name.
	String() string
}

type jsonCodec struct{}

// JSONCodec stores values as JSON and works for any type encoding/json
// supports. It is the default body codec.
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

type stringCodec struct{}

// StringCodec stores a string or []byte as-is, adding no encoding. Marshal
// rejects any other type, and Unmarshal needs a *string or *[]byte target.
var StringCodec = &stringCodec{}

func (c *stringCodec) Marshal(v any) ([]byte, error) {
	switch val := v.(type) {
	case string:
		return []byte(val), nil
	case []byte:
		return slices.Clone(val), nil
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
	StringCodec.String(): StringCodec,
	JSONCodec.String():   JSONCodec,
}
