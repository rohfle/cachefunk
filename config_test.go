package cachefunk_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/rohfle/cachefunk"
)

func TestConfigIncomplete(t *testing.T) {
	var config cachefunk.Config

	result := config.Get("doesnotexist")

	if result != cachefunk.DefaultKeyConfig {
		t.Fatalf("expected non-existent key to resolve to DefaultKeyConfig but got %+v instead (type=%T)", result, result)
	}

}

func TestConfigMarshalUnmarshal(t *testing.T) {
	var config = cachefunk.Config{
		Defaults: &cachefunk.KeyConfig{TTL: 1},
		Configs: map[string]*cachefunk.KeyConfig{
			"test": {TTL: 1},
			"test1": {
				TTL:               1,
				TTLJitter:         1,
				FallbackToExpired: true,
				BodyCodec:         cachefunk.JSONCodec,
				BodyCompression:   cachefunk.GzipCompression,
				ParamCodec:        cachefunk.JSONParams,
			},
			"test2": {
				TTL:               1,
				TTLJitter:         1,
				FallbackToExpired: true,
				BodyCodec:         cachefunk.MsgPackCodec,
				BodyCompression:   cachefunk.ZstdCompression,
				ParamCodec:        cachefunk.JSONBase64Params,
			},
			"test3": {
				TTL:               1,
				TTLJitter:         1,
				FallbackToExpired: true,
				BodyCodec:         cachefunk.StringCodec,
				BodyCompression:   cachefunk.BrotliCompression,
			},
			"test4": {
				TTL:               1,
				TTLJitter:         1,
				FallbackToExpired: true,
				BodyCodec:         cachefunk.StringCodec,
				BodyCompression:   cachefunk.NoCompression,
			},
		},
	}

	data, err := json.Marshal(&config)
	if err != nil {
		t.Fatal("error while marshaling cachefunk config:", err)
	}

	var otherConfig cachefunk.Config

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
