package cachefunk

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

func TestConfigIncomplete(t *testing.T) {
	var config Config

	result := config.Get("doesnotexist")

	if result != DefaultKeyConfig {
		t.Fatalf("expected non-existent key to resolve to DefaultKeyConfig but got %+v instead (type=%T)", result, result)
	}

}

func TestConfigMarshalUnmarshal(t *testing.T) {
	var config = Config{
		Defaults: &KeyConfig{TTL: 1},
		Configs: map[string]*KeyConfig{
			"test": {TTL: 1},
			"test1": {
				TTL:               1,
				TTLJitter:         1,
				FallbackToExpired: true,
				BodyCodec:         JSONCodec,
				BodyCompression:   GzipCompression,
				ParamCodec:        JSONParams,
			},
			"test2": {
				TTL:               1,
				TTLJitter:         1,
				FallbackToExpired: true,
				BodyCodec:         MsgPackCodec,
				BodyCompression:   ZstdCompression,
				ParamCodec:        JSONBase64Params,
			},
			"test3": {
				TTL:               1,
				TTLJitter:         1,
				FallbackToExpired: true,
				BodyCodec:         StringCodec,
				BodyCompression:   BrotliCompression,
			},
			"test4": {
				TTL:               1,
				TTLJitter:         1,
				FallbackToExpired: true,
				BodyCodec:         StringCodec,
				BodyCompression:   NoCompression,
			},
		},
	}

	data, err := json.Marshal(&config)
	if err != nil {
		t.Fatal("error while marshaling cachefunk config:", err)
	}

	var otherConfig Config

	err = json.Unmarshal(data, &otherConfig)
	if err != nil {
		t.Fatal("error while unmarshaling cachefunk config:", err)
	}

	if !reflect.DeepEqual(config, otherConfig) {
		t.Log("config is not equal to otherConfig")
		fmt.Printf("config: %+v\n", config)
		fmt.Printf("otherConfig: %+v\n", otherConfig)
		t.FailNow()
	}
}
