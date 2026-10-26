package cachefunk

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"time"
)

// Special TTL values

// TTLEntryNeverExpires means that cached values never expire
const TTLEntryNeverExpires = -1

// TTLEntryImmediatelyExpires means that cached values expire immediately: Set
// stores nothing, and Get returns no result unless FallbackToExpired is set
const TTLEntryImmediatelyExpires = 0

// Date ranges keep timestamps compatible with file system and database backends.

// MinDate is the earliest time that cachefunk uses (1970-01-01)
var MinDate = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

// MaxDate is the latest time that cachefunk uses (9999-01-01)
var MaxDate = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

// TTLMax is the largest usable TTL in seconds, about 10 years. A TTL above it
// never expires.
var TTLMax = int64(10 * (365 * 24 * time.Hour / time.Second))

// DefaultBodyCodec is the [BodyCodec] used when a key sets none.
var DefaultBodyCodec = JSONCodec

// DefaultBodyCompression is the [Compression] used when a key sets none.
var DefaultBodyCompression = ZstdCompression

// DefaultParamCodec is the [ParamCodec] used when a key sets none.
var DefaultParamCodec = JSONParams

// DefaultKey is the config key under which default settings are stored for
// cache keys that do not have their own settings.
const DefaultKey = "default"

// DefaultKeyConfig holds the settings used by keys that Config does not list.
var DefaultKeyConfig = &KeyConfig{
	TTL:               3600,
	TTLJitter:         300,
	FallbackToExpired: false,
	BodyCompression:   DefaultBodyCompression,
	BodyCodec:         DefaultBodyCodec,
	ParamCodec:        DefaultParamCodec,
}

// Config stores settings per cache key. It can be loaded directly from a JSON
// file. A nil Config is valid: every key then resolves to DefaultKeyConfig.
type Config map[string]*KeyConfig

// Get returns the settings for a cache key: the key's own entry, else the
// reserved DefaultKey entry, else the global DefaultKeyConfig. A nil entry counts
// as unset, so Get never returns a nil *KeyConfig.
func (c Config) Get(key string) *KeyConfig {
	if value, exists := c[key]; exists && value != nil {
		return value
	} else if value, exists := c[DefaultKey]; exists && value != nil {
		return value
	} else {
		return DefaultKeyConfig
	}
}

// KeyConfig holds the caching settings for one key. The zero value is not a useful
// default: a TTL of 0 expires every entry immediately.
type KeyConfig struct {
	// TTL is the lifetime of an entry in seconds.
	// A TTL of 0 expires the entry immediately.
	// A negative TTL never expires.
	// Some useful TTL values:
	// - 3600: one hour
	// - 86400: one day
	// - 604800: one week
	// - 2419200: four weeks
	// - 31536000: one year
	TTL int64
	// TTLJitter adds up to TTLJitter-1 seconds of random delay to an entry's
	// timestamp, so entries written together do not expire together. The delay is
	// capped at the TTL; see GetTTLJitter.
	TTLJitter int64
	// FallbackToExpired makes a failed retrieval return the expired entry instead
	// of the error.
	FallbackToExpired bool
	// ParamCodec serialises parameters into the entry's params. A nil value means
	// DefaultParamCodec.
	ParamCodec ParamCodec
	// BodyCodec encodes and decodes stored values. A nil value means
	// DefaultBodyCodec.
	BodyCodec BodyCodec
	// BodyCompression compresses stored values. Changing it invalidates the key's
	// existing entries. A nil value means DefaultBodyCompression.
	BodyCompression Compression
}

// GetBodyCompression returns BodyCompression, or [DefaultBodyCompression] when
// the field is nil.
func (kc *KeyConfig) GetBodyCompression() Compression {
	if kc.BodyCompression != nil {
		return kc.BodyCompression
	}
	return DefaultBodyCompression
}

// GetBodyCodec returns BodyCodec, or [DefaultBodyCodec] when the field is nil.
func (kc *KeyConfig) GetBodyCodec() BodyCodec {
	if kc.BodyCodec != nil {
		return kc.BodyCodec
	}
	return DefaultBodyCodec
}

// GetParamCodec returns ParamCodec, or [DefaultParamCodec] when the field is
// nil.
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

// GetExpireTime returns the cutoff time before which an entry counts as expired.
// The result is clamped to [MinDate] or [MaxDate] so that file system and database
// timestamps stay in range.
func (kc *KeyConfig) GetExpireTime(now time.Time) time.Time {
	if kc.TTL == TTLEntryImmediatelyExpires {
		// the cutoff is after any timestamp, so everything is expired
		return MaxDate
	} else if kc.TTL <= TTLEntryNeverExpires || kc.TTL > TTLMax {
		// the cutoff is before any timestamp, so nothing is expired
		return MinDate
	}

	secondsAvailable := int64(now.Sub(MinDate) / time.Second)
	if secondsAvailable <= kc.TTL {
		return MinDate
	}
	// GetTimestamp applies the jitter when the entry is written
	return now.Add(-1 * time.Duration(kc.TTL) * time.Second)
}

// maxJitterSeconds is the largest jitter in seconds that can be added to a
// timestamp. Fixed at one year.
const maxJitterSeconds = int64(365 * 24 * 60 * 60)

// GetTimestamp returns the timestamp of an entry with TTLJitter applied
func (kc *KeyConfig) GetTimestamp(now time.Time) time.Time {
	if jitter := kc.GetTTLJitter(); jitter > 0 {
		// add 0 to jitter-1 seconds, so an entry appears written up to jitter
		// seconds later
		delay := rand.Int64N(jitter)
		now = now.Add(time.Duration(delay) * time.Second)
	}
	return now
}

// GetTTLJitter returns the jitter applied to a timestamp: TTLJitter, capped at
// the TTL and at one year. The TTL cap means an entry lives at most twice as
// long. The year cap keeps a large TTL from carrying the timestamp out of range.
// Entries that do not expire get none.
func (kc *KeyConfig) GetTTLJitter() int64 {
	if kc.TTLJitter <= 0 || kc.TTL <= 0 {
		return 0
	}
	return min(kc.TTLJitter, kc.TTL, int64(maxJitterSeconds))
}

// MarshalJSON writes the codecs and compression as their String names, which
// UnmarshalJSON resolves back to the built-in implementations.
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

// UnmarshalJSON resolves the codec and compression names written by MarshalJSON.
// A name that is not built in leaves the field nil, so the default applies.
func (kc *KeyConfig) UnmarshalJSON(data []byte) error {
	var aux keyConfigRaw
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// unset fields stay nil; the Get methods supply the defaults
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

// DecompressAndUnmarshal undoes [KeyConfig.MarshalAndCompress]: it decompresses
// valueData with [KeyConfig.GetBodyCompression] and decodes it into value with
// [KeyConfig.GetBodyCodec].
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

// MarshalAndCompress encodes value with [KeyConfig.GetBodyCodec] and compresses
// it with [KeyConfig.GetBodyCompression], producing the bytes storage holds.
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
