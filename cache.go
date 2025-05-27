// Package cachefunk provides caching wrappers for functions
package cachefunk

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrEntryNotFound = errors.New("cache entry not found")
	ErrEntryExpired  = errors.New("cache entry expired")
)

type CtxKey string

const DefaultIgnoreCacheCtxKey CtxKey = "ignoreCache"

type CacheFunk struct {
	Config       *Config
	IgnoreCtxKey CtxKey
	Storage      CacheStorage
}

func (c *CacheFunk) GetIgnoreCtxKey() CtxKey {
	if c.IgnoreCtxKey == "" {
		return DefaultIgnoreCacheCtxKey
	}
	return c.IgnoreCtxKey
}

type CacheStorage interface {
	// Get a value from the cache if it exists
	Get(key string, config *KeyConfig, params string, expireTime time.Time) (value []byte, err error)
	// Set a raw value for key in the cache
	Set(key string, config *KeyConfig, params string, value []byte, timestamp time.Time) (err error)
	// Get the number of entries in the cache
	EntryCount() (count int64, err error)
	// Get how many entries have expired in the cache compared to expireTime
	ExpiredEntryCount(key string, config *KeyConfig, expireTime time.Time) (count int64, err error)
	// Delete all entries in the cache
	Clear() error
	// Delete entries that have timestamps in cache before expireTime
	Cleanup(key string, config *KeyConfig, expireTime time.Time) error
	// Print all cached entries for test debugging purposes
	Dump(n int64)
}

func (c *CacheFunk) Get(key string, config *KeyConfig, params string, ignoreExpireTime bool, value any) error {
	var expireTime time.Time
	if ignoreExpireTime {
		expireTime = MinDate
	} else {
		now := time.Now().UTC()
		expireTime = config.GetExpireTime(now)
	}

	valueData, err := c.Storage.Get(key, config, params, expireTime)
	if err != nil {
		return err
	}

	valueData, err = config.GetBodyCompression().Decompress(valueData)
	if err != nil {
		return fmt.Errorf("failed to decompress: %w", err)
	}

	err = config.GetBodyCodec().Unmarshal(valueData, value)
	if err != nil {
		return fmt.Errorf("failed to unmarshal: %w", err)
	}
	return nil
}

func (c *CacheFunk) Set(key string, config *KeyConfig, params string, value any) error {
	if config.TTL == TTLEntryImmediatelyExpires {
		return nil // discard the entry - do not cache
	}

	timestamp := config.GetTimestamp(time.Now().UTC())

	valueData, err := config.GetBodyCodec().Marshal(value)
	if err != nil {
		return fmt.Errorf("failed to marshal: %w", err)
	}

	valueData, err = config.GetBodyCompression().Compress(valueData)
	if err != nil {
		return fmt.Errorf("failed to compress: %w", err)
	}

	return c.Storage.Set(key, config, params, valueData, timestamp)
}

func (c *CacheFunk) EntryCount() (int64, error) {
	return c.Storage.EntryCount()
}

func (c *CacheFunk) ExpiredEntryCount() (int64, error) {
	var count int64
	now := time.Now().UTC()
	for key, config := range c.Config.Configs {
		if config.TTL == TTLEntryNeverExpires {
			continue
		}
		expireTime := config.GetExpireTime(now)
		chunk, err := c.Storage.ExpiredEntryCount(key, config, expireTime)
		if err != nil {
			return 0, fmt.Errorf("error while fetching expired entry count for key=%q: %w", key, err)
		}
		count += chunk
	}
	return count, nil
}

func (c *CacheFunk) Clear() error {
	return c.Storage.Clear()
}

func (c *CacheFunk) Cleanup() {
	now := time.Now().UTC()
	for key, config := range c.Config.Configs {
		if config.TTL == TTLEntryNeverExpires {
			continue
		}
		expireTime := config.GetExpireTime(now)
		err := c.Storage.Cleanup(key, config, expireTime)
		if err != nil {
			// deal with it
			continue
		}
	}
}

// Wrap type functions
// These don't work with type methods unfortunately
func Wrap[Params any, ResultType any](
	cache *CacheFunk,
	key string,
	retrieveFunc func(bool, Params) (ResultType, error),
) func(bool, Params) (ResultType, error) {
	return func(ignoreCache bool, params Params) (ResultType, error) {
		return Cache(cache, key, retrieveFunc, ignoreCache, params)
	}
}

func WrapWithContext[Params any, ResultType any](
	cache *CacheFunk,
	key string,
	retrieveFunc func(context.Context, Params) (ResultType, error),
) func(context.Context, Params) (ResultType, error) {
	return func(ctx context.Context, params Params) (ResultType, error) {
		return CacheWithContext(cache, key, retrieveFunc, ctx, params)
	}
}

func Cache[Params any, ResultType any](
	cache *CacheFunk,
	key string,
	resolverFunc func(bool, Params) (ResultType, error),
	ignoreCache bool,
	params Params,
) (ResultType, error) {
	config := cache.Config.Get(key)
	var result ResultType
	// serialize parameters for cache
	// key + parameters determines a unique identifier for a request
	paramStr, err := config.GetParamCodec().Marshal(params)
	if err != nil {
		// let parent handle error
		return result, fmt.Errorf("failed to marshal parameters key=%q params=%+v: %w", key, params, err)
	}
	var entryIsExpired = false
	if !ignoreCache {
		// check if theres an existing result in cache
		err := cache.Get(key, config, paramStr, false, &result)
		if err == nil {
			return result, nil
		} else if err == ErrEntryExpired {
			entryIsExpired = true
		} else if err == ErrEntryNotFound {
			// this is normal when no entry is stored
		} else {
			// some error has happened while trying to get cache value
			warning("ignoring error while getting cached result for key=%q paramStr=%+v: %s", key, paramStr, err)
		}
	}
	// either there is no existing result, or the result was invalid or expired
	// so call resolver and get a fresh result
	result, err = resolverFunc(ignoreCache, params)
	if err != nil {
		// an error has occurred
		if config.FallbackToExpired && entryIsExpired {
			// theres an expired cache entry maybe we can use it as a fallback
			// for example, if an upstream webserver disappears
			err := cache.Get(key, config, paramStr, true, &result)
			if err == nil {
				warning("falling back to expired cache result after fresh retrieval failed for key=%q paramStr=%+v: %s", key, paramStr, err)
				return result, nil
			}
		}
		// otherwise error as both the cache and the resolver failed to return a result
		return result, fmt.Errorf("failed to retrieve fresh value for key=%q paramStr=%+v: %w", key, paramStr, err)
	}
	err = cache.Set(key, config, paramStr, result)
	if err != nil {
		// passthrough the result and the error
		return result, fmt.Errorf("set cache failed for key=%q paramStr=%+v: %w", key, paramStr, err)
	}
	return result, nil
}

// CacheWithContext caches responses of any json serializable type.
func CacheWithContext[Params any, ResultType any](
	cache *CacheFunk,
	key string,
	resolverFunc func(ctx context.Context, params Params) (ResultType, error),
	ctx context.Context,
	params Params,
) (ResultType, error) {
	config := cache.Config.Get(key)
	var result ResultType
	// serialize parameters for cache
	// key + parameters determines a unique identifier for a request
	paramStr, err := config.GetParamCodec().Marshal(params)
	if err != nil {
		// let parent handle error
		return result, fmt.Errorf("failed to marshal parameters key=%q params=%+v: %w", key, params, err)
	}
	var entryIsExpired = false

	if ignoreCache, ok := ctx.Value(cache.GetIgnoreCtxKey()).(bool); !ok || !ignoreCache {
		// check if theres an existing result in cache
		err := cache.Get(key, config, paramStr, false, &result)
		if err == nil {
			return result, nil
		} else if err == ErrEntryExpired {
			entryIsExpired = true
		} else if err == ErrEntryNotFound {
			// this is normal when no entry is stored
		} else {
			// some error has happened while trying to get cache value
			warning("ignoring error while getting cached result for key=%q paramStr=%+v: %s", key, paramStr, err)
		}
	}
	// either there is no existing result, or the result was invalid or expired
	// so call resolver and get a fresh result
	result, err = resolverFunc(ctx, params)
	if err != nil {
		// an error has occurred
		if config.FallbackToExpired && entryIsExpired {
			// theres an expired cache entry maybe we can use it as a fallback
			// for example, if an upstream webserver disappears
			err := cache.Get(key, config, paramStr, true, &result)
			if err == nil {
				warning("falling back to expired cache result after fresh retrieval failed for key=%q paramStr=%+v: %s", key, paramStr, err)
				return result, nil
			}
		}
		// otherwise error as both the cache and the resolver failed to return a result
		return result, fmt.Errorf("failed to retrieve fresh value for key=%q paramStr=%+v: %s", key, paramStr, err)
	}
	err = cache.Set(key, config, paramStr, result)
	if err != nil {
		// passthrough the result and the error
		return result, fmt.Errorf("set cache failed for key=%q paramStr=%+v: %w", key, paramStr, err)
	}
	return result, nil
}
