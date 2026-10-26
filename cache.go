// Package cachefunk caches the results of function calls.
//
// The cache identifies each call by a key and its serialised parameters. The
// first call runs the function and stores the result. Later calls return the
// stored result until it expires. Concurrent misses for the same key and
// parameters share one call.
//
// Each key has a [KeyConfig] controlling its TTL, fallback, codecs and
// compression, and a [CacheStorage] decides where entries are kept.
package cachefunk

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

var (
	// ErrEntryNotFound is returned when there is no entry for the key and params.
	ErrEntryNotFound = errors.New("cache entry not found")

	// ErrEntryExpired is returned when an entry exists but has passed its expiry
	// time. Storage keeps the entry, so a caller with FallbackToExpired can use the
	// stale value.
	ErrEntryExpired = errors.New("cache entry expired")

	// ErrDoNotCache is returned by a resolver whose result is valid but must not
	// be cached. The caller receives the result anyway and nothing is stored.
	ErrDoNotCache = errors.New("do not cache the entry")

	// ErrReentrantCall is returned when a resolver calls back into the cache for the
	// same key and params it is resolving, using the context it was given.
	ErrReentrantCall = errors.New("re-entrant cache call on same key and params")
)

// LazyLoad decodes a cached entry into value, which must be a pointer to the
// result type. [CacheFunk.GetLazy] returns one so the caller decodes only when it
// needs the value.
type LazyLoad func(any) error

// ErrCannotSerialise is returned when a value cannot be represented in the cache
// at all, such as an unsupported result type for the configured BodyCodec. Unlike
// storage failures, this is deterministic so callers get the error rather than
// it being warned about and ignored.
var ErrCannotSerialise = errors.New("value cannot be serialised")

type ignoreCacheKey struct{}

// flightMarker is a context key; one distinct key per in-flight key and params.
type flightMarker string

// GetIgnoreCacheFromContext reports whether ctx was marked by
// [SetIgnoreCacheInContext]. [CacheWithContext] and [WrapWithContext] honour the
// mark; an unmarked context reads as false.
func GetIgnoreCacheFromContext(ctx context.Context) bool {
	ignoreCache, _ := ctx.Value(ignoreCacheKey{}).(bool)
	return ignoreCache
}

// SetIgnoreCacheInContext returns a copy of ctx that makes [CacheWithContext]
// and [WrapWithContext] skip reading the cache.
func SetIgnoreCacheInContext(ctx context.Context, value bool) context.Context {
	return context.WithValue(ctx, ignoreCacheKey{}, value)
}

// DefaultStorageTimeout bounds a storage call made on behalf of a flight, which
// is detached from any caller's deadline. Without a bound, a hung connection
// blocks every waiter on that key.
const DefaultStorageTimeout = 5 * time.Second

// CacheFunk is a cache in front of a [CacheStorage]. It is safe for concurrent
// use by multiple goroutines and must not be copied after first use.
type CacheFunk struct {
	// Config holds the settings for each key. Keys without an entry use
	// DefaultKeyConfig.
	Config Config
	// Storage stores the cached values. A nil Storage panics on first use.
	Storage CacheStorage
	// StorageTimeout bounds storage calls made on behalf of a flight. Zero uses
	// DefaultStorageTimeout; a negative value leaves them unbounded.
	StorageTimeout time.Duration

	// flights collapses concurrent misses for the same key and params into
	// one upstream call. The zero value is ready to use.
	flights singleflight.Group

	// faults accumulates the counters that FaultCounts reports.
	faults faultCounts
}

type faultCounts struct {
	readErrors      atomic.Int64
	decodeErrors    atomic.Int64
	writeErrors     atomic.Int64
	serialiseErrors atomic.Int64
}

// FaultCounts are cumulative counts of cache side failures, as returned by
// [CacheFunk.FaultCounts].
type FaultCounts struct {
	// ReadErrors counts storage reads that failed.
	ReadErrors int64
	// DecodeErrors counts cached values that could not be decoded.
	DecodeErrors int64
	// WriteErrors counts values that could not be stored. The caller is not told
	// about these; the resolved value is returned uncached.
	WriteErrors int64
	// SerialiseErrors counts values that could not be serialised for storage,
	// which is also reported to the caller as an error.
	SerialiseErrors int64
}

// CacheStorage is where [CacheFunk] keeps entries. [InMemoryStorage],
// [DiskStorage] and [GORMStorage] implement it.
//
// Implementations must be safe for concurrent use by multiple goroutines. They
// must honour ctx where the backend allows, return ctx.Err() or an error
// wrapping it once the call is cancelled, and must not retain ctx after
// returning. A method that ignores ctx remains conformant but cannot be
// interrupted.
type CacheStorage interface {
	// Get returns the entry stored for key and params. A missing entry returns
	// ErrEntryNotFound. An expired entry returns ErrEntryExpired, along with the
	// value when FallbackToExpired is set. An entry written with a different
	// compression than the key's configured compression counts as missing.
	Get(ctx context.Context, key string, config *KeyConfig, params string, expireBeforeTime time.Time) (value []byte, err error)

	// Set stores value for key and params, recording timestamp as the write time.
	Set(ctx context.Context, key string, config *KeyConfig, params string, value []byte, timestamp time.Time) (err error)

	// Keys returns the distinct cache keys that have entries. A cancelled call
	// returns nil and ctx.Err(), never a partial slice.
	Keys(ctx context.Context) (keys []string, err error)

	// EntryCount returns the number of entries in the cache. A cancelled call
	// returns zero and ctx.Err().
	EntryCount(ctx context.Context) (count int64, err error)

	// ExpiredEntryCount returns the number of entries under key whose timestamp is
	// before expireBeforeTime. A cancelled call returns zero and ctx.Err().
	ExpiredEntryCount(ctx context.Context, key string, config *KeyConfig, expireBeforeTime time.Time) (count int64, err error)

	// Clear deletes every entry in the cache.
	Clear(ctx context.Context) error

	// Cleanup deletes the entries under key whose timestamp is before
	// expireBeforeTime. A cancelled call may leave the work partly done; it
	// returns an error wrapping ctx.Err().
	Cleanup(ctx context.Context, key string, config *KeyConfig, expireBeforeTime time.Time) error
}

// GetLazy returns a decoder for the entry at key and params. A missing entry
// returns [ErrEntryNotFound]. If the entry has expired and FallbackToExpired is
// set, GetLazy returns the decoder along with [ErrEntryExpired].
func (c *CacheFunk) GetLazy(ctx context.Context, key string, config *KeyConfig, params string) (LazyLoad, error) {
	expireBeforeTime := config.GetExpireTime(time.Now().UTC())

	valueData, err := c.Storage.Get(ctx, key, config, params, expireBeforeTime)
	useExpired := (err == ErrEntryExpired && config.FallbackToExpired)
	if err != nil && !useExpired {
		return nil, err
	}

	lazyload := func(value any) error {
		return config.DecompressAndUnmarshal(valueData, value)
	}
	return lazyload, err
}

// Get decodes the entry at key and params into value, which must be a pointer to
// the result type. A miss returns [ErrEntryNotFound]. An expired entry returns
// [ErrEntryExpired], decoding value only when FallbackToExpired is set.
func (c *CacheFunk) Get(ctx context.Context, key string, config *KeyConfig, params string, value any) error {
	expireBeforeTime := config.GetExpireTime(time.Now().UTC())

	valueData, err := c.Storage.Get(ctx, key, config, params, expireBeforeTime)
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

// Set encodes value and stores it at key and params, timestamped by
// [KeyConfig.GetTimestamp]. A value the configured codec cannot encode returns an
// error wrapping [ErrCannotSerialise]. A TTL of TTLEntryImmediatelyExpires
// without FallbackToExpired stores nothing and returns nil.
func (c *CacheFunk) Set(ctx context.Context, key string, config *KeyConfig, params string, value any) error {
	if config.TTL == TTLEntryImmediatelyExpires && !config.FallbackToExpired {
		return nil // discard the entry - do not cache
	}

	timestamp := config.GetTimestamp(time.Now().UTC())

	valueData, err := config.MarshalAndCompress(value)
	if err != nil {
		// a value that cannot be represented in the cache is a problem with the
		// result type or codec rather than the storage, so it is marked as such
		return fmt.Errorf("%w: %w", ErrCannotSerialise, err)
	}

	return c.Storage.Set(ctx, key, config, params, valueData, timestamp)
}

// FaultCounts returns the current counters. Each field is read atomically; the
// four are not a snapshot of one instant. Failures the cache absorbs are
// otherwise only visible in the warning log.
func (c *CacheFunk) FaultCounts() FaultCounts {
	return FaultCounts{
		ReadErrors:      c.faults.readErrors.Load(),
		DecodeErrors:    c.faults.decodeErrors.Load(),
		WriteErrors:     c.faults.writeErrors.Load(),
		SerialiseErrors: c.faults.serialiseErrors.Load(),
	}
}

// EntryCount returns the number of entries across all keys, including expired
// entries that [CacheFunk.Cleanup] has not removed yet.
func (c *CacheFunk) EntryCount(ctx context.Context) (int64, error) {
	return c.Storage.EntryCount(ctx)
}

// ExpiredEntryCount returns how many entries have passed their expiry time,
// across all keys. Keys that never expire are skipped.
func (c *CacheFunk) ExpiredEntryCount(ctx context.Context) (int64, error) {
	var count int64
	now := time.Now().UTC()
	keys, err := c.Storage.Keys(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
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
		chunk, err := c.Storage.ExpiredEntryCount(ctx, key, config, expireBeforeTime)
		if err != nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			return 0, fmt.Errorf("error while fetching expired entry count for key=%q: %w", key, err)
		}
		count += chunk
	}
	return count, nil
}

// storageTimeout returns the bound for flight-scoped storage calls, or zero when
// bounds are disabled. A zero StorageTimeout means DefaultStorageTimeout.
func (c *CacheFunk) storageTimeout() time.Duration {
	switch {
	case c.StorageTimeout == 0:
		return DefaultStorageTimeout
	case c.StorageTimeout < 0:
		return 0
	default:
		return c.StorageTimeout
	}
}

// storageContext bounds parent by the effective storage timeout, or returns it
// unchanged when bounds are disabled.
func (c *CacheFunk) storageContext(parent context.Context) (context.Context, context.CancelFunc) {
	if d := c.storageTimeout(); d > 0 {
		return context.WithTimeout(parent, d)
	}
	return parent, func() {}
}

// Clear removes every entry, across all keys.
func (c *CacheFunk) Clear(ctx context.Context) error {
	return c.Storage.Clear(ctx)
}

// Cleanup deletes the expired entries of every key in storage. It skips keys that
// never expire. It also skips keys with FallbackToExpired unless
// forceCleanupExpired is set, because their expired entries can answer a failed
// retrieval. Per-key failures become warnings; cancellation returns ctx.Err() and
// may leave the work partly done.
func (c *CacheFunk) Cleanup(ctx context.Context, forceCleanupExpired bool) error {
	now := time.Now().UTC()
	keys, err := c.Storage.Keys(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		warning("failed to list cache keys for cleanup: %s", err)
		return nil
	}
	// Iterate over keys present in Storage rather than keys present in Config,
	// so entries under defaults are cleaned up also
	for _, key := range keys {
		config := c.Config.Get(key)
		if config.TTL == TTLEntryNeverExpires || (config.FallbackToExpired && !forceCleanupExpired) {
			continue
		}
		expireBeforeTime := config.GetExpireTime(now)
		if err := c.Storage.Cleanup(ctx, key, config, expireBeforeTime); err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("cleanup cancelled at key=%q: %w", key, ctx.Err())
			}
			warning("failed to cleanup cache key=%q: %s", key, err)
		}
	}
	return nil
}

// Wrap returns retrieveFunc bound to cache under key, the curried form of
// [Cache]. Go methods cannot have their own type parameters, so the wrapper is a
// function rather than a [CacheFunk] method.
func Wrap[Params any, ResultType any](
	cache *CacheFunk,
	key string,
	retrieveFunc func(Params) (ResultType, error),
) func(Params) (ResultType, error) {
	return func(params Params) (ResultType, error) {
		return Cache(cache, key, retrieveFunc, params)
	}
}

// WrapWithIgnore is [Wrap] for a resolver that also takes ignoreCache, so each
// call can skip reading the cache. The retrieved result is still stored.
func WrapWithIgnore[Params any, ResultType any](
	cache *CacheFunk,
	key string,
	retrieveFunc func(bool, Params) (ResultType, error),
) func(bool, Params) (ResultType, error) {
	return func(ignoreCache bool, params Params) (ResultType, error) {
		return CacheWithIgnore(cache, key, retrieveFunc, ignoreCache, params)
	}
}

// WrapWithContext is [Wrap] for a resolver that takes a context.Context. Whether
// the call ignores the cache is read from that context; see
// [SetIgnoreCacheInContext].
func WrapWithContext[Params any, ResultType any](
	cache *CacheFunk,
	key string,
	retrieveFunc func(context.Context, Params) (ResultType, error),
) func(context.Context, Params) (ResultType, error) {
	return func(ctx context.Context, params Params) (ResultType, error) {
		return CacheWithContext(cache, key, retrieveFunc, ctx, params)
	}
}

// Cache returns the fresh entry at key and params, or calls resolverFunc,
// stores its result and returns it. Concurrent misses for the same key and
// params share a single resolverFunc call.
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

// CacheWithIgnore is [Cache] with ignoreCache. The call skips reading the cache
// but joins an in-flight retrieval for the same key and params. The result is
// stored.
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

// CacheWithContext is [Cache] with ctx passed to resolverFunc and used to bound
// waiting. The resolver runs detached from ctx's cancellation, so one caller
// giving up does not fail the others sharing the retrieval. Each caller stops
// waiting when its own ctx ends. A context marked by [SetIgnoreCacheInContext]
// skips reading the cache. The resolver and the flight's storage calls see ctx's
// values but not its cancellation, and those storage calls are bounded by
// [CacheFunk.StorageTimeout].
func CacheWithContext[Params any, ResultType any](
	cache *CacheFunk,
	key string,
	resolverFunc func(context.Context, Params) (ResultType, error),
	ctx context.Context,
	params Params,
) (ResultType, error) {
	return cacheImpl(ctx, cache, key, resolverFunc, GetIgnoreCacheFromContext(ctx), params)
}

// shortParams trims serialised params for a log line as the params can be kilobytes long
func shortParams(params string) string {
	const max = 128
	if len(params) <= max {
		return params
	}
	return fmt.Sprintf("%s... (%d bytes)", params[:max], len(params))
}

// tryCache returns hit=true with a decoded result for a fresh entry. If only an
// expired entry is available and FallbackToExpired is set, lazyload is non-nil
// so the caller can use it as a fallback. err is non-nil only when ctx was
// cancelled or timed out during the read, leaving the owner of ctx to decide
// whether that counts as a fault.
func tryCache[R any](ctx context.Context, cache *CacheFunk, key string, config *KeyConfig, paramStr string) (result R, lazyload LazyLoad, hit bool, err error) {
	lazyload, gerr := cache.GetLazy(ctx, key, config, paramStr)
	if gerr == nil {
		if derr := lazyload(&result); derr == nil {
			return result, nil, true, nil
		} else {
			// Fresh entry exists but won't decode. Return a nil lazyload so the
			// caller can't "fall back" to the same undecodable bytes: a retry would
			// fail identically and only mask the corruption.
			cache.faults.decodeErrors.Add(1)
			warning("ignoring error while decoding cached result for key=%q paramStr=%s: %s", key, shortParams(paramStr), derr)
			var zero R
			return zero, nil, false, nil
		}
	}
	if cerr := ctx.Err(); cerr != nil {
		return result, nil, false, cerr
	}
	if gerr != ErrEntryNotFound && gerr != ErrEntryExpired {
		cache.faults.readErrors.Add(1)
		warning("ignoring error while getting cached result for key=%q paramStr=%s: %s", key, shortParams(paramStr), gerr)
	}
	// ErrEntryExpired with FallbackToExpired: lazyload is non-nil and usable
	return result, lazyload, false, nil
}

// cacheImpl looks up key and params in the cache and, on a miss, calls
// resolverFunc. Concurrent misses for the same key and params share a single
// resolver call (singleflight). Each caller waits on its own ctx; the resolver
// itself runs on a context detached from any individual caller's cancellation.
//
// A resolver must not call the cache for the same key and params it is resolving.
// Context-aware callers that pass the resolver's context through get
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
		return zero, fmt.Errorf("failed to marshal parameters key=%q params=%+v: %w", key, params, err)
	}

	// Fast path: fresh cache hit, no coordination needed. A caller that is
	// already gone still gets an instant hit, so that read is detached and
	// bounded; the resolve below is skipped either way.
	if !ignoreCache {
		readCtx := ctx
		if ctx.Err() != nil {
			var cancel context.CancelFunc
			readCtx, cancel = cache.storageContext(context.WithoutCancel(ctx))
			defer cancel()
		}
		result, _, hit, terr := tryCache[ResultType](readCtx, cache, key, config, paramStr)
		if terr != nil {
			// the caller's context is done: return before starting a resolve
			return zero, terr
		}
		if hit {
			return result, nil
		}
	}

	// Don't join or start a flight if the caller is already gone
	if err := ctx.Err(); err != nil {
		return zero, fmt.Errorf("context done before fetching key=%q paramStr=%+v: %w", key, paramStr, err)
	}

	flightKey := fmt.Sprintf("%d:%s%s", len(key), key, paramStr)

	// Called from inside our own resolver (same key and params): joining the flight
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

		// Detached from the leader's cancellation (values are preserved), so one
		// caller going away doesn't fail everyone else waiting on this flight.
		// The marker lets nested calls on the same key and params detect re-entrancy.
		// Note the leader's deadline is dropped too: resolvers should enforce
		// their own timeouts.
		flightCtx := context.WithValue(context.WithoutCancel(ctx), flightMarker(flightKey), struct{}{})

		// Re-check the cache in case a previous flight just populated it. The read
		// stays bounded: a hung storage would otherwise block every waiter.
		var fallback LazyLoad
		if !ignoreCache {
			readCtx, cancel := cache.storageContext(flightCtx)
			result, lazyload, hit, terr := tryCache[ResultType](readCtx, cache, key, config, paramStr)
			cancel()
			if terr != nil {
				// the bound fired, so this is a storage fault and not a caller leaving
				cache.faults.readErrors.Add(1)
				warning("storage timed out reading key=%q paramStr=%s: %s", key, shortParams(paramStr), terr)
			} else if hit {
				return result, nil
			}
			fallback = lazyload
		}

		result, rerr := resolverFunc(flightCtx, params)
		switch {
		case rerr == nil:
			// call to resolver was successful, set value in cache. The write stays
			// bounded so a hung storage cannot keep the waiters blocked either
			writeCtx, cancel := cache.storageContext(flightCtx)
			serr := cache.Set(writeCtx, key, config, paramStr, result)
			cancel()
			if serr != nil {
				if errors.Is(serr, ErrCannotSerialise) {
					// this value will never be cacheable
					cache.faults.serialiseErrors.Add(1)
					return result, fmt.Errorf("set cache failed for key=%q paramStr=%s: %w", key, shortParams(paramStr), serr)
				}
				// a write to storage that fails should return the valid value to the caller
				// as failing here would result in retries of the same endpoint anyway
				cache.faults.writeErrors.Add(1)
				warning("failed to cache result for key=%q paramStr=%s: %s", key, shortParams(paramStr), serr)
			}
			return result, nil
		case errors.Is(rerr, ErrDoNotCache):
			// the result is valid, but do not cache the result
			// useful for things like gateway timeout in http, where you want to return the response
			// but it doesn't make sense to use the same response next time
			// errors.Is rather than == so a resolver wrapping the error with %w is honoured too
			return result, nil
		default:
			if config.FallbackToExpired && fallback != nil {
				// there's an expired cache entry, maybe we can use it as a fallback
				// for example, if an upstream webserver disappears
				var stale ResultType
				if lerr := fallback(&stale); lerr == nil {
					warning("falling back to expired cache result after fresh retrieval failed for key=%q paramStr=%s: %s", key, shortParams(paramStr), rerr)
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
