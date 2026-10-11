package cachefunk

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"time"
)

// Special TTL values

// TTLEntryNeverExpires means that cached values will never expire
const TTLEntryNeverExpires = -1

// TTLEntryImmediatelyExpires means that cached values immediately expire
// All cache Sets will do nothing and all non-fallback Gets return no result
const TTLEntryImmediatelyExpires = 0

// Date ranges
// This allows compatibility with different storage backends including
// modtimes on modern filesystems, databases and so on

// MinDate is the earliest time that cachefunk uses (1970-01-01)
var MinDate = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

// MaxDate is the latest time that cachefunk uses (9999-01-01)
var MaxDate = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
var TTLMax = int64(MaxDate.Sub(MinDate) / time.Second)

// Defaults

var DefaultBodyCodec = JSONCodec
var DefaultBodyCompression = ZstdCompression
var DefaultParamCodec = JSONParams

// DefaultKey is the config key under which default settings are stored for
// cache keys that do not have their own settings.
const DefaultKey = "default"

// DefaultKeyConfig holds the settings for any key that does not have config defined
var DefaultKeyConfig = &KeyConfig{
	TTL:               3600,
	TTLJitter:         300,
	FallbackToExpired: false,
	BodyCompression:   DefaultBodyCompression,
	BodyCodec:         DefaultBodyCodec,
	ParamCodec:        DefaultParamCodec,
}

// Config stores settings for different cached endpoints by key.
// It is a map that can be loaded directly from a JSON file.
// A nil Config is valid: every key then resolves to DefaultKeyConfig.
type Config map[string]*KeyConfig

// Get returns the cache config given a key: first the key's own entry, then
// if that does not exist the reserved DefaultKey entry. If DefaultKey entry
// does not exist then the global DefaultKeyConfig will be returned.
// A nil entry counts as unset, so this never returns a nil *KeyConfig.
func (c Config) Get(key string) *KeyConfig {
	if value, exists := c[key]; exists && value != nil {
		return value
	} else if value, exists := c[DefaultKey]; exists && value != nil {
		return value
	} else {
		return DefaultKeyConfig
	}
}

// KeyConfig is a set of specific settings for a cached endpoint
type KeyConfig struct {
	// TTL is time to live in seconds before the cache value can be deleted
	// If TTL is 0, cache value will expire immediately
	// If TTL is less than 0, cache value will "never" expire (not until year 9999)
	// Some useful TTL values:
	// - 3600: one hour
	// - 86400: one day
	// - 604800: one week
	// - 2419200: four weeks
	// - 31536000: one year
	TTL int64
	// When TTLJitter is > 0, a random value from 0 to TTLJitter-1 seconds is added
	// to the timestamp of the entry. This spreads cache expire time to stop
	// retrieval of fresh responses all at once. The jitter applied is capped at
	// the TTL, see GetTTLJitter.
	TTLJitter int64
	// Use expired entries when a fresh value cannot be successfully retrieved
	FallbackToExpired bool
	// Settings for transformation of parameters
	ParamCodec ParamCodec
	// Settings for transformation of body
	BodyCodec       BodyCodec
	BodyCompression Compression
}

func (kc *KeyConfig) GetBodyCompression() Compression {
	if kc.BodyCompression != nil {
		return kc.BodyCompression
	}
	return DefaultBodyCompression
}

func (kc *KeyConfig) GetBodyCodec() BodyCodec {
	if kc.BodyCodec != nil {
		return kc.BodyCodec
	}
	return DefaultBodyCodec
}

func (kc *KeyConfig) GetParamCodec() ParamCodec {
	if kc.ParamCodec != nil {
		return kc.ParamCodec
	}
	return DefaultParamCodec
}

type keyConfigRaw struct {
	TTL               int64  `json:"ttl"`
	TTLJitter         int64  `json:"ttl_jitter,omitempty"`
	FallbackToExpired bool   `json:"fallback_to_expired,omitempty"`
	ParamCodec        string `json:"param_codec,omitempty"`
	BodyCodec         string `json:"body_codec,omitempty"`
	BodyCompression   string `json:"body_compression,omitempty"`
}

// GetExpireTime calculates an expire time from the key config
// within reasonable bounds for modern filesystems (as of year 2017)
// any entry with timestamp before the expire time is said to have expired
func (kc *KeyConfig) GetExpireTime(now time.Time) time.Time {
	if kc.TTL == TTLEntryImmediatelyExpires {
		// expireBeforeTime will always be after timestamp therefore everything expires immediately
		return MaxDate
	} else if kc.TTL <= TTLEntryNeverExpires || kc.TTL > TTLMax {
		// expireBeforeTime will always be before timestamp therefore everything never expires
		return MinDate
	}

	secondsAvailable := int64(now.Sub(MinDate) / time.Second)
	if secondsAvailable <= kc.TTL {
		return MinDate
	}
	// Jitter is taken care of at timestamp generation during cache set
	return now.Add(-1 * time.Duration(kc.TTL) * time.Second)
}

// maxJitterSeconds is the largest jitter in seconds that can be added to a
// timestamp. Hard coded at one year
const maxJitterSeconds = int64(365 * 24 * 60 * 60)

// GetTimestamp returns the timestamp of an entry with TTLJitter applied
func (kc *KeyConfig) GetTimestamp(now time.Time) time.Time {
	if jitter := kc.GetTTLJitter(); jitter > 0 {
		// a random value from 0 to jitter-1 seconds is added
		// the effect is the timestamp will appear to be created
		// up to jitter seconds into the future
		delay := rand.Int64N(jitter)
		now = now.Add(time.Duration(delay) * time.Second)
	}
	return now
}

// GetTTLJitter returns the jitter that will be applied to a timestamp: TTLJitter
// capped so it can never extend an entry's life past two times its TTL, and at
// one year so that a large TTL cannot carry the timestamp out of range.
// Entries that do not expire get no jitter.
func (kc *KeyConfig) GetTTLJitter() int64 {
	if kc.TTLJitter <= 0 || kc.TTL <= 0 {
		return 0
	}
	return min(kc.TTLJitter, kc.TTL, int64(maxJitterSeconds))
}

// MarshalJSON helps to marshal the different codecs and compression
func (kc *KeyConfig) MarshalJSON() ([]byte, error) {
	var bodyCodec string
	var bodyCompression string
	var paramCodec string

	if kc.BodyCodec != nil {
		bodyCodec = kc.BodyCodec.String()
	}
	if kc.BodyCompression != nil {
		bodyCompression = kc.BodyCompression.String()
	}
	if kc.ParamCodec != nil {
		paramCodec = kc.ParamCodec.String()
	}

	var aux = &keyConfigRaw{
		TTL:               kc.TTL,
		TTLJitter:         kc.TTLJitter,
		FallbackToExpired: kc.FallbackToExpired,
		BodyCodec:         bodyCodec,
		BodyCompression:   bodyCompression,
		ParamCodec:        paramCodec,
	}

	return json.Marshal(&aux)
}

// UnmarshalJSON helps to unmarshal the different codecs and compression
func (kc *KeyConfig) UnmarshalJSON(data []byte) error {
	var aux keyConfigRaw
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// these will be nil when unset, but this is dealt with elsewhere
	bodyCodec := codecMap[aux.BodyCodec]
	bodyCompression := compressionMap[aux.BodyCompression]
	paramCodec := paramMap[aux.ParamCodec]

	kc.TTL = aux.TTL
	kc.TTLJitter = aux.TTLJitter
	kc.FallbackToExpired = aux.FallbackToExpired
	kc.BodyCodec = bodyCodec
	kc.BodyCompression = bodyCompression
	kc.ParamCodec = paramCodec
	return nil
}

func (kc *KeyConfig) DecompressAndUnmarshal(valueData []byte, value any) error {
	valueData, err := kc.GetBodyCompression().Decompress(valueData)
	if err != nil {
		return fmt.Errorf("failed to decompress: %w", err)
	}

	err = kc.GetBodyCodec().Unmarshal(valueData, value)
	if err != nil {
		return fmt.Errorf("failed to unmarshal: %w", err)
	}
	return nil
}

func (kc *KeyConfig) MarshalAndCompress(value any) ([]byte, error) {
	valueData, err := kc.GetBodyCodec().Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal: %w", err)
	}

	valueData, err = kc.GetBodyCompression().Compress(valueData)
	if err != nil {
		return nil, fmt.Errorf("failed to compress: %w", err)
	}
	return valueData, nil
}
