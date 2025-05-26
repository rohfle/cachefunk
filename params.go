package cachefunk

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

type ParamCodec interface {
	Marshal(v any) (string, error)
	Unmarshal(data string, v any) error
	String() string
}

type ParameterEncoder func(interface{}) (string, error)

type jsonParams struct{}

var JSONParams = &jsonParams{}

func (c *jsonParams) Marshal(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("while marshaling %+v as json: %w", v, err)
	}
	return string(raw), err
}

func (c *jsonParams) Unmarshal(data string, v any) error {
	raw := []byte(data)
	return json.Unmarshal(raw, v)
}

func (c *jsonParams) String() string {
	return "json"
}

type jsonBase64Params struct{}

var JSONBase64Params = &jsonBase64Params{}

func (c *jsonBase64Params) Marshal(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("while marshaling %+v as json: %w", v, err)
	}
	return base64.URLEncoding.EncodeToString(raw), err
}

func (c *jsonBase64Params) Unmarshal(data string, v any) error {
	raw, err := base64.URLEncoding.DecodeString(data)
	if err != nil {
		return fmt.Errorf("while decoding base64 string %+v: %w", data, err)
	}
	return json.Unmarshal(raw, v)
}

func (c *jsonBase64Params) String() string {
	return "json+base64"
}

var paramMap = map[string]ParamCodec{
	JSONParams.String():       JSONParams,
	JSONBase64Params.String(): JSONBase64Params,
}
