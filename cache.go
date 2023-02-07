// This library gives you wrapper functions to cache function output in golang
package cachefunk

import (
	"encoding/json"
	"math/rand"
	"time"
)

// Cache is an interface that supports get/set of values by key
type Cache interface {
	// Get a value from the cache if it exists
	Get(config *Config, params string) (value []byte, found bool)
	// Set a value in the cache
	Set(config *Config, params string, value []byte)
	// Get the number of entries in the cache
	EntryCount() int64
	// Get how many entries have expired in the cache compared to cutoff
	// entries expiry compared to utc now if cutoff is nil
	ExpiredEntryCount(cutoff *time.Time) int64
	// Delete all entries in the cache
	Clear()
	// Delete entries that have expired in the cache compared to cutoff
	// entries expiry compared to utc now if cutoff is nil
	Cleanup(cutoff *time.Time)
}

// Config is used to configure the caching wrapper functions
type Config struct {
	// Key is used with params to create an identifier to get / set cache values
	// It should be set to a unique value for each function that is wrapped
	Key string
	// TTL is time to live in seconds before the cache value can be deleted
	// If TTL is 0, cache value will expire immediately
	// If TTL is -1, cache value will never expire
	TTL int64
	// When TTLJitter is > 0, a random value from 1 to TTLJitter will be added to TTL
	// This spreads cache expiry out to stop getting fresh responses all at once
	TTLJitter int64
}

// renderParameters returns a string representation of params
func renderParameters(params interface{}) (string, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// calculateExpiryTime calculates the expiry time using TTL
func calculateExpiryTime(config *Config) *time.Time {
	if config.TTL < 0 {
		return nil // cache indefinitely
	}

	ttl := config.TTL
	if config.TTLJitter > 0 {
		// randomize TTL
		ttl += rand.Int63n(config.TTLJitter) + 1
	}
	expiresAt := time.Now().Add(time.Duration(ttl) * time.Second).UTC()
	return &expiresAt
}

// WrapString is a function wrapper that caches string or []byte responses.
func WrapString[Params any, ResultType string | []byte](
	retrieveFunc func(bool, Params) (ResultType, error),
	cache Cache,
	config Config,
) func(bool, Params) (ResultType, error) {
	return func(ignoreCache bool, params Params) (ResultType, error) {
		// serialize parameters for cache
		// key + parameters determines a unique identifier for a request
		var result ResultType
		paramsRendered, err := renderParameters(params)
		if err != nil {
			return result, err
		}

		if !ignoreCache {
			// Look for existing value in cache
			value, found := cache.Get(&config, paramsRendered)
			if found {
				return ResultType(value), nil
			}
		}
		value, err := retrieveFunc(ignoreCache, params)
		if err != nil {
			return value, err
		}
		cache.Set(&config, paramsRendered, []byte(value))
		return value, nil
	}
}

// Wrap is a function wrapper that caches responses of any json serializable type.
func Wrap[Params any, ResultType any](
	retrieveFunc func(bool, Params) (ResultType, error),
	cache Cache,
	config Config,
) func(bool, Params) (ResultType, error) {
	return func(ignoreCache bool, params Params) (ResultType, error) {
		// serialize parameters for cache
		// key + parameters determines a unique identifier for a request
		var result ResultType
		paramsRendered, err := renderParameters(params)
		if err != nil {
			return result, err
		}
		if !ignoreCache {
			// Look for existing value in cache
			value, found := cache.Get(&config, paramsRendered)
			if found {
				var result ResultType
				if err := json.Unmarshal(value, &result); err == nil {
					// Errors during unmarshal are ignored because the invalid cached value
					// will be overwritten by a fresh response anyway
					return result, nil
				}
			}
		}
		result, err = retrieveFunc(ignoreCache, params)
		if err != nil {
			return result, err
		}
		value, err := json.Marshal(result)
		if err != nil {
			return result, err
		}
		cache.Set(&config, paramsRendered, value)
		return result, nil
	}
}
