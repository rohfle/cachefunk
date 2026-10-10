package cachefunk

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func TestConfigIncomplete(t *testing.T) {
	var config Config

	result := config.Get("doesnotexist")

	if result != DefaultKeyConfig {
		t.Fatalf("expected non-existent key to resolve to DefaultKeyConfig but got %+v instead (type=%T)", result, result)
	}

}

// TestConfigGetConcurrent hammers Config.Get on a fresh Config from many
// goroutines. Its real value is under `go test -race`: a reintroduced write to
// the config map would be reported as a data race.
func TestConfigGetConcurrent(t *testing.T) {
	cfg := Config{}

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if got := cfg.Get("key"); got == nil {
					t.Errorf("Config.Get returned nil")
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestConfigDefaultKey checks the resolution order: the key's own entry, then
// the reserved DefaultKey entry, then DefaultKeyConfig.
func TestConfigDefaultKey(t *testing.T) {
	var empty Config
	if got := empty.Get("hello"); got != DefaultKeyConfig {
		t.Fatalf("nil Config: expected DefaultKeyConfig but got %+v", got)
	}

	cacheWide := &KeyConfig{TTL: 60}
	config := Config{DefaultKey: cacheWide}
	if got := config.Get("hello"); got != cacheWide {
		t.Fatalf("expected the DefaultKey entry for an unconfigured key but got %+v", got)
	}
	if got := config.Get(DefaultKey); got != cacheWide {
		t.Fatalf("expected the DefaultKey entry for DefaultKey itself but got %+v", got)
	}

	own := &KeyConfig{TTL: 300}
	config["hello"] = own
	if got := config.Get("hello"); got != own {
		t.Fatalf("expected the key's own entry to take precedence over DefaultKey but got %+v", got)
	}
}

// TestConfigJSONShape pins the JSON wire format: a flat object keyed by cache
// key, with DefaultKey holding the cache-wide defaults.
func TestConfigJSONShape(t *testing.T) {
	config := Config{
		DefaultKey:   {TTL: 60},
		"hello":      {TTL: 3600},
		"withJitter": {TTL: 1, TTLJitter: 5},
	}

	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("error while marshaling config: %s", err)
	}

	const want = `{"default":{"ttl":60},"hello":{"ttl":3600},"withJitter":{"ttl":1,"ttl_jitter":5}}`
	if string(data) != want {
		t.Fatalf("config JSON shape changed:\n got: %s\nwant: %s", data, want)
	}
}

func TestConfigMarshalUnmarshal(t *testing.T) {
	var config = Config{
		DefaultKey: &KeyConfig{TTL: 1},
		"test":     {TTL: 1},
		"test1": {
			TTL:               1,
			TTLJitter:         1,
			FallbackToExpired: true,
			BodyCodec:         JSONCodec,
			BodyCompression:   ZstdCompression,
			ParamCodec:        JSONParams,
		},
		"test2": {
			TTL:               1,
			TTLJitter:         1,
			FallbackToExpired: true,
			BodyCodec:         JSONCodec,
			BodyCompression:   ZstdCompression,
			ParamCodec:        JSONBase64Params,
		},
		"test3": {
			TTL:               1,
			TTLJitter:         1,
			FallbackToExpired: true,
			BodyCodec:         StringCodec,
			BodyCompression:   ZstdCompression,
		},
		"test4": {
			TTL:               1,
			TTLJitter:         1,
			FallbackToExpired: true,
			BodyCodec:         StringCodec,
			BodyCompression:   NoCompression,
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
