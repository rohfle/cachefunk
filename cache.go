// Package cachefunk provides caching wrappers for functions
package cachefunk

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sync/singleflight"
)

var (
	ErrEntryNotFound = errors.New("cache entry not found")
	ErrEntryExpired  = errors.New("cache entry expired")
	ErrDoNotCache    = errors.New("do not cache the entry")

	// ErrReentrantCall is returned when a resolver calls back into the cache with the
	// same key+params it is currently resolving, using the context it was given.
	ErrReentrantCall = errors.New("re-entrant cache call on same key and params")
)

type LazyLoad func(any) error

type ignoreCacheKey struct{}

// flightMarker is a context key; one distinct key per in-flight key+params.
type flightMarker string

func GetIgnoreCacheFromContext(ctx context.Context) bool {
	ignoreCache, _ := ctx.Value(ignoreCacheKey{}).(bool)
	return ignoreCache
}

func SetIgnoreCacheInContext(ctx context.Context, value bool) context.Context {
	return context.WithValue(ctx, ignoreCacheKey{}, value)
}

type CacheFunk struct {
	Config  Config
	Storage CacheStorage

	// flights collapses concurrent cache misses for the same key+params into a
	// single upstream call. The zero value is ready to use. Do not copy a
	// CacheFunk by value after first use.
	flights singleflight.Group
}

type CacheStorage interface {
	// Get a value from the cache if it exists
	Get(key string, config *KeyConfig, params string, expireBeforeTime time.Time) (value []byte, err error)
	// Set a raw value for key in the cache
	Set(key string, config *KeyConfig, params string, value []byte, timestamp time.Time) (err error)
	// Get the distinct cache keys that currently have entries in the cache
	Keys() (keys []string, err error)
	// Get the number of entries in the cache
	EntryCount() (count int64, err error)
	// Get how many entries have expired in the cache compared to expireBeforeTime
	ExpiredEntryCount(key string, config *KeyConfig, expireBeforeTime time.Time) (count int64, err error)
	// Delete all entries in the cache
	Clear() error
	// Delete entries that have timestamps in cache before expireBeforeTime
	Cleanup(key string, config *KeyConfig, expireBeforeTime time.Time) error
}

func (c *CacheFunk) GetLazy(key string, config *KeyConfig, params string) (LazyLoad, error) {
	expireBeforeTime := config.GetExpireTime(time.Now().UTC())

	valueData, err := c.Storage.Get(key, config, params, expireBeforeTime)
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
	expireBeforeTime := config.GetExpireTime(time.Now().UTC())

	valueData, err := c.Storage.Get(key, config, params, expireBeforeTime)
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
	if config.TTL == TTLEntryImmediatelyExpires && !config.FallbackToExpired {
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
	keys, err := c.Storage.Keys()
	if err != nil {
		return 0, fmt.Errorf("error while fetching cache keys: %w", err)
	}
	// Iterate over keys present in Storage rather than keys present in Config,
	// so entries under defaults are counted too
	for _, key := range keys {
		config := c.Config.Get(key)
		if config.TTL == TTLEntryNeverExpires {
			continue
		}
		expireBeforeTime := config.GetExpireTime(now)
		chunk, err := c.Storage.ExpiredEntryCount(key, config, expireBeforeTime)
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

func (c *CacheFunk) Cleanup(forceCleanupExpired bool) {
	now := time.Now().UTC()
	keys, err := c.Storage.Keys()
	if err != nil {
		warning("failed to list cache keys for cleanup: %s", err)
		return
	}
	// Iterate over keys present in Storage rather than keys present in Config,
	// so entries under defaults are cleaned up also
	for _, key := range keys {
		config := c.Config.Get(key)
		if config.TTL == TTLEntryNeverExpires || (config.FallbackToExpired && !forceCleanupExpired) {
			continue
		}
		expireBeforeTime := config.GetExpireTime(now)
		err := c.Storage.Cleanup(key, config, expireBeforeTime)
		if err != nil {
			warning("failed to cleanup cache key=%q: %s", key, err)
		}
	}
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
	adapted := func(_ context.Context, p Params) (ResultType, error) {
		return resolverFunc(p)
	}
	return cacheImpl(context.Background(), cache, key, adapted, false, params)
}

func CacheWithIgnore[Params any, ResultType any](
	cache *CacheFunk,
	key string,
	resolverFunc func(bool, Params) (ResultType, error),
	ignoreCache bool,
	params Params,
) (ResultType, error) {
	adapted := func(_ context.Context, p Params) (ResultType, error) {
		return resolverFunc(ignoreCache, p)
	}
	return cacheImpl(context.Background(), cache, key, adapted, ignoreCache, params)
}

func CacheWithContext[Params any, ResultType any](
	cache *CacheFunk,
	key string,
	resolverFunc func(context.Context, Params) (ResultType, error),
	ctx context.Context,
	params Params,
) (ResultType, error) {
	return cacheImpl(ctx, cache, key, resolverFunc, GetIgnoreCacheFromContext(ctx), params)
}

// tryCache returns hit=true with a decoded result for a fresh entry. If only an
// expired entry is available and FallbackToExpired is set, lazyload is non-nil
// so the caller can use it as a fallback.
func tryCache[R any](cache *CacheFunk, key string, config *KeyConfig, paramStr string) (result R, lazyload LazyLoad, hit bool) {
	lazyload, err := cache.GetLazy(key, config, paramStr)
	if err == nil {
		if err = lazyload(&result); err == nil {
			return result, nil, true
		}
		// Fresh entry exists but won't decode. Return a nil lazyload so the
		// caller can't "fall back" to the same undecodable bytes: a retry would
		// fail identically and only mask the corruption.
		warning("ignoring error while decoding cached result for key=%q paramStr=%+v: %s", key, paramStr, err)
		var zero R
		return zero, nil, false
	}
	if err != ErrEntryNotFound && err != ErrEntryExpired {
		warning("ignoring error while getting cached result for key=%q paramStr=%+v: %s", key, paramStr, err)
	}
	// ErrEntryExpired with FallbackToExpired: lazyload is non-nil and usable
	return result, lazyload, false
}

// cacheImpl looks up key+params in the cache and, on a miss, calls resolverFunc.
// Concurrent misses for the same key+params share a single resolver call
// (singleflight). Each caller waits on its own ctx; the resolver itself runs on
// a context detached from any individual caller's cancellation.
//
// A resolver must not call the cache for the same key+params it is currently
// resolving. Context-aware callers that pass the resolver's context through get
// ErrReentrantCall; ctxless callers will wait on themselves (forever, since
// there is no deadline).
func cacheImpl[Params any, ResultType any](
	ctx context.Context,
	cache *CacheFunk,
	key string,
	resolverFunc func(context.Context, Params) (ResultType, error),
	ignoreCache bool,
	params Params,
) (ResultType, error) {
	config := cache.Config.Get(key)
	var zero ResultType

	// serialize parameters for cache
	// key + parameters determines a unique identifier for a request
	paramStr, err := config.GetParamCodec().Marshal(params)
	if err != nil {
		// let parent handle error
		return zero, fmt.Errorf("failed to marshal parameters key=%q params=%+v: %w", key, params, err)
	}

	// Fast path: fresh cache hit, no coordination needed
	if !ignoreCache {
		if result, _, hit := tryCache[ResultType](cache, key, config, paramStr); hit {
			return result, nil
		}
	}

	// Don't join or start a flight if the caller is already gone
	if err := ctx.Err(); err != nil {
		return zero, fmt.Errorf("context done before fetching key=%q paramStr=%+v: %w", key, paramStr, err)
	}

	flightKey := fmt.Sprintf("%d:%s%s", len(key), key, paramStr)

	// Called from inside our own resolver (same key+params): joining the flight
	// would wait on ourselves forever.
	if ctx.Value(flightMarker(flightKey)) != nil {
		return zero, fmt.Errorf("%w: key=%q paramStr=%+v", ErrReentrantCall, key, paramStr)
	}

	ch := cache.flights.DoChan(flightKey, func() (v any, ferr error) {
		// With DoChan the function runs in its own goroutine, and a panic there
		// would crash the process (singleflight can't forward it to waiters).
		// Convert panics to errors instead.
		defer func() {
			if r := recover(); r != nil {
				v, ferr = nil, fmt.Errorf("panic while retrieving key=%q paramStr=%+v: %v", key, paramStr, r)
			}
		}()

		// Re-check the cache in case a previous flight just populated it
		var fallback LazyLoad
		if !ignoreCache {
			result, lazyload, hit := tryCache[ResultType](cache, key, config, paramStr)
			if hit {
				return result, nil
			}
			fallback = lazyload
		}

		// Detached from the leader's cancellation (values are preserved), so one
		// caller going away doesn't fail everyone else waiting on this flight.
		// The marker lets nested calls on the same key+params detect re-entrancy.
		// Note the leader's deadline is dropped too: resolvers should enforce
		// their own timeouts.
		flightCtx := context.WithValue(context.WithoutCancel(ctx), flightMarker(flightKey), struct{}{})

		result, rerr := resolverFunc(flightCtx, params)
		switch {
		case rerr == nil:
			// call to resolver was successful, set value in cache
			if serr := cache.Set(key, config, paramStr, result); serr != nil {
				// passthrough result and error to parent
				return result, fmt.Errorf("set cache failed for key=%q paramStr=%+v: %w", key, paramStr, serr)
			}
			return result, nil
		case errors.Is(rerr, ErrDoNotCache):
			// the result is valid, but do not cache the result
			// useful for things like gateway timeout in http, where you want to return the response
			// but it doesnt make sense to use the same response next time
			// errors.Is rather than == so a resolver wrapping the error with %w is honoured too
			return result, nil
		default:
			if config.FallbackToExpired && fallback != nil {
				// there's an expired cache entry, maybe we can use it as a fallback
				// for example, if an upstream webserver disappears
				var stale ResultType
				if lerr := fallback(&stale); lerr == nil {
					warning("falling back to expired cache result after fresh retrieval failed for key=%q paramStr=%+v: %s", key, paramStr, rerr)
					return stale, nil
				}
			}
			// otherwise error as both the cache and the resolver failed to return a result
			return result, fmt.Errorf("failed to retrieve fresh value for key=%q paramStr=%+v: %w", key, paramStr, rerr)
		}
	})

	// Prefer a result that is already available over a simultaneous cancellation.
	select {
	case res := <-ch:
		result, _ := res.Val.(ResultType) // zero value if Val is nil
		return result, res.Err
	default:
	}

	select {
	case <-ctx.Done():
		// This caller leaves; the flight keeps running for the others and will
		// still populate the cache when it finishes.
		return zero, fmt.Errorf("context done while waiting for key=%q paramStr=%+v: %w", key, paramStr, ctx.Err())
	case res := <-ch:
		result, _ := res.Val.(ResultType)
		return result, res.Err
	}
}
