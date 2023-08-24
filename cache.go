// This library gives you wrapper functions to cache function output in golang
package cachefunk

import (
	"context"
	"encoding/json"
	"time"
)

type CtxKey string

const DEFAULT_IGNORE_CACHE_CTX_KEY CtxKey = "ignoreCache"

// Cache is an interface that supports get/set of values by key
type Cache interface {
	SetConfig(config *CacheFunkConfig)
	// Get a value from the cache if it exists
	Get(key string, params string) (value []byte, found bool)
	// Set a value in the cache
	Set(key string, params string, value []byte)
	// Set a raw value for key in the cache
	SetRaw(key string, params string, value []byte, timestamp time.Time, isCompressed bool)
	// Get the number of entries in the cache
	EntryCount() int64
	// Get how many entries have expired in the cache compared to cutoff
	// entries expiry compared to utc now if cutoff is nil
	ExpiredEntryCount() int64
	// Delete all entries in the cache
	Clear()
	// Delete entries that have timestamps in cache before cutoff
	// entries expiry compared to utc now if cutoff is nil
	Cleanup()
	// GetIgnoreCacheCtxKey returns Value key under which ignoreCache is stored
	GetIgnoreCacheCtxKey() CtxKey
}

// renderParameters returns a string representation of params
func RenderParameters(params interface{}) (string, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// Wrap type functions
// These don't work with type methods unfortunately

// WrapObjects is a function wrapper that caches responses of any json serializable type.
func WrapObject[Params any, ResultType any](
	cache Cache,
	key string,
	retrieveFunc func(bool, Params) (ResultType, error),
) func(bool, Params) (ResultType, error) {
	return func(ignoreCache bool, params Params) (ResultType, error) {
		return CacheObject(cache, key, retrieveFunc, ignoreCache, params)
	}
}

// WrapString is a function wrapper that caches string or []byte responses.
func WrapString[Params any, ResultType string | []byte](
	cache Cache,
	key string,
	retrieveFunc func(bool, Params) (ResultType, error),
) func(bool, Params) (ResultType, error) {
	return func(ignoreCache bool, params Params) (ResultType, error) {
		return CacheString(cache, key, retrieveFunc, ignoreCache, params)
	}
}

// WrapObjectWithContext is a function wrapper that caches responses of any json serializable type.
func WrapObjectWithContext[Params any, ResultType any](
	cache Cache,
	key string,
	retrieveFunc func(context.Context, Params) (ResultType, error),
) func(context.Context, Params) (ResultType, error) {
	return func(ctx context.Context, params Params) (ResultType, error) {
		return CacheObjectWithContext(cache, key, retrieveFunc, ctx, params)
	}
}

// WrapStringWithContext is a function wrapper that caches string or []byte responses.
func WrapStringWithContext[Params any, ResultType string | []byte](
	cache Cache,
	key string,
	retrieveFunc func(context.Context, Params) (ResultType, error),
) func(context.Context, Params) (ResultType, error) {
	return func(ctx context.Context, params Params) (ResultType, error) {
		return CacheStringWithContext(cache, key, retrieveFunc, ctx, params)
	}
}

// Cache functions
// Less pretty than wrappers but they work with type methods

func CacheString[Params any, ResultType string | []byte](
	cache Cache,
	key string,
	retrieveFunc func(bool, Params) (ResultType, error),
	ignoreCache bool,
	params Params,
) (ResultType, error) {
	// serialize parameters for cache
	// key + parameters determines a unique identifier for a request
	var result ResultType
	paramsRendered, err := RenderParameters(params)
	if err != nil {
		return result, err
	}

	if !ignoreCache {
		// Look for existing value in cache
		value, found := cache.Get(key, paramsRendered)
		if found {
			return ResultType(value), nil
		}
	}
	value, err := retrieveFunc(ignoreCache, params)
	if err != nil {
		return value, err
	}
	cache.Set(key, paramsRendered, []byte(value))
	return value, nil
}

// CacheObject is a function wrapper that caches responses of any json serializable type.
func CacheObject[Params any, ResultType any](
	cache Cache,
	key string,
	retrieveFunc func(bool, Params) (ResultType, error),
	ignoreCache bool,
	params Params,
) (ResultType, error) {
	// serialize parameters for cache
	// key + parameters determines a unique identifier for a request
	var result ResultType
	paramsRendered, err := RenderParameters(params)
	if err != nil {
		return result, err
	}
	if !ignoreCache {
		// Look for existing value in cache
		value, found := cache.Get(key, paramsRendered)
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
	cache.Set(key, paramsRendered, value)
	return result, nil
}

// CacheWithStringContext caches string or []byte responses.
func CacheStringWithContext[Params any, ResultType string | []byte](
	cache Cache,
	key string,
	retrieveFunc func(ctx context.Context, params Params) (ResultType, error),
	ctx context.Context,
	params Params,
) (ResultType, error) {
	// serialize parameters for cache
	// key + parameters determines a unique identifier for a request
	var result ResultType
	paramsRendered, err := RenderParameters(params)
	if err != nil {
		return result, err
	}
	if ignoreCache, ok := ctx.Value(cache.GetIgnoreCacheCtxKey()).(bool); !ok || !ignoreCache {
		// Look for existing value in cache
		value, found := cache.Get(key, paramsRendered)
		if found {
			return ResultType(value), nil
		}
	}
	value, err := retrieveFunc(ctx, params)
	if err != nil {
		return value, err
	}
	cache.Set(key, paramsRendered, []byte(value))
	return value, nil
}

// CacheWithContext caches responses of any json serializable type.
func CacheObjectWithContext[Params any, ResultType any](
	cache Cache,
	key string,
	retrieveFunc func(ctx context.Context, params Params) (ResultType, error),
	ctx context.Context,
	params Params,
) (ResultType, error) {
	// serialize parameters for cache
	// key + parameters determines a unique identifier for a request
	var result ResultType
	paramsRendered, err := RenderParameters(params)
	if err != nil {
		return result, err
	}
	if ignoreCache, ok := ctx.Value(cache.GetIgnoreCacheCtxKey()).(bool); !ok || !ignoreCache {
		// Look for existing value in cache
		value, found := cache.Get(key, paramsRendered)
		if found {
			var result ResultType
			if err := json.Unmarshal(value, &result); err == nil {
				// Errors during unmarshal are ignored because the invalid cached value
				// will be overwritten by a fresh response anyway
				return result, nil
			}
		}
	}
	result, err = retrieveFunc(ctx, params)
	if err != nil {
		return result, err
	}
	value, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	cache.Set(key, paramsRendered, value)
	return result, nil
}
