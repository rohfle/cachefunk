package cachefunk

import (
	"encoding/json"
	"math/rand/v2"
	"time"
)

// special values for TTL
// cached values never expire
const NEVER_EXPIRES = -1

// cached values immediately expire
const IMMEDIATELY_EXPIRES = 0

var DefaultBodyCodec = JSONCodec
var DefaultBodyCompression = ZstdCompression
var DefaultParamCodec = JSONParams

var DefaultKeyConfig = &KeyConfig{
	TTL:               3600,
	TTLJitter:         300,
	FallbackToExpired: false,
	BodyCompression:   DefaultBodyCompression,
	BodyCodec:         DefaultBodyCodec,
	ParamCodec:        DefaultParamCodec,
}

type Config struct {
	Defaults *KeyConfig
	Configs  map[string]*KeyConfig
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

func (c *Config) Get(key string) *KeyConfig {
	if c.Configs == nil {
		c.Configs = make(map[string]*KeyConfig)
	}
	if value, exists := c.Configs[key]; exists {
		return value
	} else if c.Defaults != nil {
		return c.Defaults
	} else {
		return DefaultKeyConfig
	}
}

// Config is used to configure the caching wrapper functions
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
	// When TTLJitter is > 0, a random value from 1 to TTLJitter will be added to TTL
	// This spreads cache expire time to stop retrieval of fresh responses all at once
	TTLJitter int64
	// Use expired entries when a fresh value cannot be successfully retrieved
	FallbackToExpired bool
	// Settings for transformation of parameters
	ParamCodec ParamCodec
	// Settings for transformation of body
	BodyCodec       BodyCodec
	BodyCompression Compression
}

type keyConfigRaw struct {
	TTL               int64  `json:"ttl"`
	TTLJitter         int64  `json:"ttl_jitter,omitempty"`
	FallbackToExpired bool   `json:"fallback_to_expired,omitempty"`
	ParamCodec        string `json:"param_codec,omitempty"`
	BodyCodec         string `json:"body_codec,omitempty"`
	BodyCompression   string `json:"body_compression,omitempty"`
}

var CACHEFUNK_MIN_DATE = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
var CACHEFUNK_MAX_DATE = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)
var CACHEFUNK_MAX_TTL = int64(CACHEFUNK_MAX_DATE.Sub(CACHEFUNK_MIN_DATE) / time.Second)

// Get expireTime based on ttl
// with reasonable bounds for modern filesystems (year 2017)
func (kc *KeyConfig) GetExpireTime(now time.Time) time.Time {
	if kc.TTL == IMMEDIATELY_EXPIRES {
		// TTL is invalid texpireTime will always be after timestamp therefore everything expires immediately
		return CACHEFUNK_MAX_DATE
	} else if kc.TTL <= NEVER_EXPIRES || kc.TTL > CACHEFUNK_MAX_TTL {
		// expireTime will always be before timestamp therefore never expires
		return CACHEFUNK_MIN_DATE
	}

	secondsAvailable := int64(now.Sub(CACHEFUNK_MIN_DATE) / time.Second)
	if secondsAvailable <= kc.TTL {
		return CACHEFUNK_MIN_DATE
	}
	// Jitter is taken care of at timestamp generation during cache set
	return now.Add(-1 * time.Duration(kc.TTL) * time.Second)
}

func (kc *KeyConfig) GetTimestamp(now time.Time) time.Time {
	if kc.TTLJitter > 0 {
		// a random value from 1 to TTLJitter seconds is added
		// the effect is the timestamp will appear to be created
		// up to TTLJitter seconds into the future
		delay := rand.Int64N(kc.TTLJitter)
		now = now.Add(time.Duration(delay) * time.Second)
	}
	return now
}

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
