package cachefunk

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
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

// TestTTLJitterCap checks the two jitter caps. Jitter extends an entry's life by
// at most its TTL. The year cap keeps a TTL in centuries from carrying the
// timestamp out of range.
func TestTTLJitterCap(t *testing.T) {
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	oneYear := time.Duration(maxJitterSeconds) * time.Second

	for _, tc := range []struct {
		name       string
		ttl        int64
		jitter     int64
		wantJitter int64
		wantMax    time.Duration
	}{
		{"under the cap", 3600, 300, 300, 300 * time.Second},
		{"at the cap", 3600, 3600, 3600, 3600 * time.Second},
		{"over the cap", 3600, 1 << 62, 3600, 3600 * time.Second},
		{"ttl at the limit", TTLMax, TTLMax, 365 * 24 * 60 * 60, oneYear},
		{"ttl of one second", 1, 5, 1, 0},
		{"immediate expiry", TTLEntryImmediatelyExpires, 300, 0, 0},
		{"never expires", TTLEntryNeverExpires, 300, 0, 0},
		{"no jitter", 3600, 0, 0, 0},
	} {
		config := &KeyConfig{TTL: tc.ttl, TTLJitter: tc.jitter}

		if got := config.GetTTLJitter(); got != tc.wantJitter {
			t.Fatalf("%s: expected an effective jitter of %d seconds but got %d", tc.name, tc.wantJitter, got)
		}

		var maxApplied time.Duration
		for range 100 {
			applied := config.GetTimestamp(now).Sub(now)
			if applied < 0 || applied > tc.wantMax {
				t.Fatalf("%s: expected jitter between 0 and %s but got %s", tc.name, tc.wantMax, applied)
			}
			maxApplied = max(maxApplied, applied)
		}

		if tc.wantMax > 0 && maxApplied == 0 {
			t.Errorf("%s: expected jitter to be applied but every timestamp was unjittered", tc.name)
		}
	}
}

// TestGetExpireTimeTTLLimit checks the TTLMax policy: a TTL above the limit never
// expires, and a TTL at the limit is honoured. The unit is pinned too: TTLMax is
// in seconds, like TTL. A nanosecond constant would make every TTL look like
// "never expires".
func TestGetExpireTimeTTLLimit(t *testing.T) {
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)

	above := (&KeyConfig{TTL: TTLMax + 1}).GetExpireTime(now)
	if !above.Equal(MinDate) {
		t.Errorf("expected a TTL above TTLMax (%d) to never expire but got %s", TTLMax, above)
	}

	at := &KeyConfig{TTL: TTLMax}
	below := at.GetExpireTime(now)
	if below.Equal(MinDate) {
		t.Fatalf("expected a TTL at TTLMax (%d seconds) to expire, not to never expire", TTLMax)
	}
	if want := now.Add(-time.Duration(at.TTL) * time.Second); !below.Equal(want) {
		t.Errorf("expected a TTL at TTLMax to expire at %s but got %s", want, below)
	}
}

// TestConfigNilEntry checks that a nil entry (a null value in a JSON config file)
// counts as unset. It must not reach the caller as a nil *KeyConfig, which used to
// panic when a cached call read the config.
func TestConfigNilEntry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config Config
	}{
		{"key", Config{"hello": nil}},
		{"default", Config{DefaultKey: nil}},
		{"key and default", Config{DefaultKey: nil, "hello": nil}},
	} {
		if got := tc.config.Get("hello"); got != DefaultKeyConfig {
			t.Errorf("%s: expected a nil entry to resolve to DefaultKeyConfig but got %+v", tc.name, got)
		}
	}

	// the config a JSON file with a null value unmarshals to, plus the cached
	// call that used to dereference it
	var config Config
	if err := json.Unmarshal([]byte(`{"default": {"ttl": 60}, "hello": null}`), &config); err != nil {
		t.Fatalf("error while unmarshaling config: %s", err)
	}
	if got := config.Get("hello"); got != config[DefaultKey] {
		t.Fatalf("expected the null key to resolve to the default entry but got %+v", got)
	}

	cache := &CacheFunk{Config: config, Storage: NewInMemoryStorage()}
	value, err := Wrap(cache, "hello", func(params string) (string, error) {
		return "hello " + params, nil
	})("bob")
	if err != nil {
		t.Fatalf("cached call under a null config entry returned an error: %s", err)
	}
	if value != "hello bob" {
		t.Fatalf("expected \"hello bob\" but got %q", value)
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
