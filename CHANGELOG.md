# Changelog

Behaviour changes per release, newest first. The README describes current
behaviour; an entry below describes the release that introduced a change, in the
terminology of that release.

## 0.6.0

- Breaking: every `CacheStorage` method takes a `context.Context` first. A call
  cancelled before it starts, or one the backend can interrupt, returns
  `ctx.Err()` or an error wrapping it. A single file operation already under way
  may finish and return nil. Custom implementations must add the parameter and
  check it on entry. `storagetest.Run` checks the contract, including
  cancellation
- Breaking: `CacheFunk.Get`, `GetLazy`, `Set`, `EntryCount`, `ExpiredEntryCount`
  and `Clear` take a context, and `Cleanup` takes one and returns an error so a
  cancelled cleanup is visible rather than only warned about
- Storage reads and writes are bounded by `CacheFunk.StorageTimeout`
  (`DefaultStorageTimeout`, 5 seconds, by default): flight-scoped calls made on
  everyone's behalf, and a caller's own read when its context has no deadline.
  A timeout is a warning and a fault count, never a failed call
- A caller that gives up stops waiting without cancelling the resolver or the
  write behind it. A caller that was already gone still receives a live cache hit
- `Keys` and the count methods return zero values with `ctx.Err()` rather than
  partial results. `Cleanup` may leave its work partly done and says so in the
  error
- New `storagetest` package: `storagetest.Run(t, open)` exercises the whole
  storage contract against a supplied backend

## 0.5.0

- Upgrading from 0.4.x: the cache table used by the GORM storage must be dropped,
  and `Config` is now keyed by cache key
- Config is now a map keyed by cache key, with the reserved `default` entry
  holding the settings for every key that has no entry of its own. When no
  `default` entry is present, it falls back to the global `DefaultKeyConfig`
- Removed brotli, msgpack and gzip support
- Removed the unused `ParameterEncoder`, and made `DiskStorage.Dump`,
  `InMemoryStorage.Dump`, `GORMStorage.Dump` and `DiskStorage.IterateFiles`
  private. `Dump` is no longer part of `CacheStorage` interface
- The database storage indexes a sha256 of the params instead of the params
  themselves, as an index entry can only hold about 2704 bytes in PostgreSQL.
  This is a schema change on top of the table drop above
- Coalesce concurrent cache misses into one retrieve to prevent stampedes, with
  per caller context handling
- Return ErrReentrantCall for nested calls for the same key and params. Otherwise the
  `singleflight` group will deadlock
- Return resolver panics as errors to callers waiting on the `singleflight` group
- A failed cache write no longer fails the call: the resolved value is returned
  uncached and a warning is logged, so a full disk or an unavailable database
  degrades to no caching rather than to no service. An unserialisable value still
  returns an error as `ErrCannotSerialise`, since it will always be the same result
- Cap TTLJitter to the TTL, so jitter cannot extend an entry's life past two times
  its TTL
- Honour ErrDoNotCache when a resolver wraps it with an error
- Treat a nil entry in Config as unset rather than panicking
- Quote column names in GORM storage queries, for databases where key is reserved
- Added a (key, timestamp) index so cleanup and expired counting are index scans,
  and dropped the implicit transaction around each write. `AutoMigrate` creates
  the index on the first run after upgrading
- Cleanup and ExpiredEntryCount enumerate the keys that have entries in storage
  rather than the keys listed in Config, so entries under a key with no
  configuration are counted and cleaned up too
- DiskStorage cleanup and expired counting no longer act on a key that escapes the
  cache root
- Cache hits decode in parallel: the zstd decoder concurrency limit was dropped
- StringCodec.Marshal no longer aliases the caller's []byte
- Module now requires go 1.26, adding golang.org/x/sync for the flight group
- Warning messages print at most the first 128 bytes of the serialised params
- `CacheFunk.FaultCounts` reports cumulative cache side failure counts (storage
  read, decode, write and serialisation) so an absorbed failure can be alerted on
  without depending on the warning log
- Warnings are printed at most once per `DefaultWarningInterval` per format
  string, with the number suppressed reported on the next one: a failing cache
  warns once an interval instead of once per call. `SetWarningInterval` changes or
  disables the interval

## 0.4.2

- Added ErrDoNotCache to let the target function return a result that should not
  be cached

## 0.4.1

- Cache TTLEntryImmediatelyExpires entries when FallbackToExpired is set
- Added lazy load of existing expired entries
- Fallback to Expired
- Changed Wrap to WrapWithIgnore, Cache to CacheWithIgnore
- Removed ability set ignore cache ctx key
- Ensure DiskStorage file paths are within the cache

## 0.4.0

- Complete rewrite
- Compression and Codec methods are now per config key
- Removed string / object specific functions, now unified type handling
- Added zstd, brotli, msgpack support
- Added warning log, DisableWarnings and EnableWarnings function

## 0.3.0

- Added disk cache
- Changed from storing expire time to timestamp when entry was cached
- Added gzip compression
- Changed CacheResult to CacheObject, CacheWithContext to CacheObjectWithContext
- Moved TTL configuration to cache initialization function
- Removed TTL value for store indefinitely
- Messed around with git version tags to try to erase history

## 0.2.0

- Created CacheResult, CacheString, CacheWithContext, CacheStringWithContext
  functions

## 0.1.0

- Initial release
