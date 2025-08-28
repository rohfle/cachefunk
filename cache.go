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

type LazyLoad func(any) error
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

func (c *CacheFunk) GetLazy(key string, config *KeyConfig, params string) (LazyLoad, error) {
	expireTime := config.GetExpireTime(time.Now().UTC())

	valueData, err := c.Storage.Get(key, config, params, expireTime)
	useExpired := (err == ErrEntryExpired && config.FallbackToExpired)
	if err != nil && !useExpired {
		return nil, err
	}

	lazyload := func(value any) error {
		return config.DecompressAndUnmarshal(valueData, value)
	}
	return lazyload, err
}

func (c *CacheFunk) Get(key string, config *KeyConfig, params string, value any) error {
	expireTime := config.GetExpireTime(time.Now().UTC())

	valueData, err := c.Storage.Get(key, config, params, expireTime)
	useExpired := (err == ErrEntryExpired && config.FallbackToExpired)
	if err != nil && !useExpired {
		return err
	}

	err = config.DecompressAndUnmarshal(valueData, value)
	if err != nil {
		return err
	} else if useExpired {
		return ErrEntryExpired
	}
	return nil
}

func (c *CacheFunk) Set(key string, config *KeyConfig, params string, value any) error {
	if config.TTL == TTLEntryImmediatelyExpires {
		return nil // discard the entry - do not cache
	}

	timestamp := config.GetTimestamp(time.Now().UTC())

	valueData, err := config.MarshalAndCompress(value)
	if err != nil {
		return err
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

func (c *CacheFunk) GetIgnoreCacheFromContext(ctx context.Context) bool {
	ignoreCache, _ := ctx.Value(c.GetIgnoreCtxKey()).(bool)
	return ignoreCache
}

// Wrap type functions
// These don't work with type methods unfortunately
func Wrap[Params any, ResultType any](
	cache *CacheFunk,
	key string,
	retrieveFunc func(Params) (ResultType, error),
) func(Params) (ResultType, error) {
	return func(params Params) (ResultType, error) {
		return Cache(cache, key, retrieveFunc, params)
	}
}

func WrapWithIgnore[Params any, ResultType any](
	cache *CacheFunk,
	key string,
	retrieveFunc func(bool, Params) (ResultType, error),
) func(bool, Params) (ResultType, error) {
	return func(ignoreCache bool, params Params) (ResultType, error) {
		return CacheWithIgnore(cache, key, retrieveFunc, ignoreCache, params)
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
	resolverFunc func(Params) (ResultType, error),
	params Params,
) (ResultType, error) {
	resolverFuncPatch := func(params Params) (ResultType, error) {
		return resolverFunc(params)
	}

	return cacheImpl(cache, key, resolverFuncPatch, false, params)
}

func CacheWithIgnore[Params any, ResultType any](
	cache *CacheFunk,
	key string,
	resolverFunc func(bool, Params) (ResultType, error),
	ignoreCache bool,
	params Params,
) (ResultType, error) {
	resolverFuncPatch := func(params Params) (ResultType, error) {
		return resolverFunc(ignoreCache, params)
	}

	return cacheImpl(cache, key, resolverFuncPatch, ignoreCache, params)
}

func CacheWithContext[Params any, ResultType any](
	cache *CacheFunk,
	key string,
	resolverFunc func(context.Context, Params) (ResultType, error),
	ctx context.Context,
	params Params,
) (ResultType, error) {
	ignoreCache := cache.GetIgnoreCacheFromContext(ctx)
	resolverFuncPatch := func(params Params) (ResultType, error) {
		return resolverFunc(ctx, params)
	}

	return cacheImpl(cache, key, resolverFuncPatch, ignoreCache, params)
}

func cacheImpl[Params any, ResultType any](
	cache *CacheFunk,
	key string,
	resolverFunc func(Params) (ResultType, error),
	ignoreCache bool,
	params Params,
) (ResultType, error) {
	config := cache.Config.Get(key)
	var result ResultType
	var lazyload func(any) error = nil
	// serialize parameters for cache
	// key + parameters determines a unique identifier for a request
	paramStr, err := config.GetParamCodec().Marshal(params)
	if err != nil {
		// let parent handle error
		return result, fmt.Errorf("failed to marshal parameters key=%q params=%+v: %w", key, params, err)
	}

	if !ignoreCache {
		// check if theres an existing result in cache
		lazyload, err = cache.GetLazy(key, config, paramStr)
		if err == nil {
			// result is good and not expired, immediately lazyload
			if err = lazyload(&result); err == nil {
				// decompress and unmarshal successful
				return result, nil
			}
		}

		if err != nil && err != ErrEntryNotFound && err != ErrEntryExpired {
			// there has been an error either in GetLazy or calling lazyload
			warning("ignoring error while getting cached result for key=%q paramStr=%+v: %s", key, paramStr, err)
		}
	}
	// either there is no existing result, or the result was invalid or expired
	// so call resolver and get a fresh result
	result, err = resolverFunc(params)
	if err != nil {
		// an error has occurred
		if config.FallbackToExpired && lazyload != nil {
			// theres an expired cache entry maybe we can use it as a fallback
			// for example, if an upstream webserver disappears
			if err = lazyload(&result); err == nil {
				// decompress and unmarshal successful
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
