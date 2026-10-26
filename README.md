# cachefunk

Cache function call results in Go, with TTLs, jitter, compression and three storage backends.

[![Test](https://github.com/rohfle/cachefunk/actions/workflows/test.yml/badge.svg)](https://github.com/rohfle/cachefunk/actions/workflows/test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/rohfle/cachefunk.svg)](https://pkg.go.dev/github.com/rohfle/cachefunk)

## Features

- Three storage backends: in memory, on disk, or in a GORM database (PostgreSQL or SQLite)
- Per key TTL with optional jitter
- Fallback to an expired entry when the resolver fails
- JSON or raw-string results with optional zstd compression
- One resolver call per key and params, however many callers miss at once
- Cache bypass per call by boolean or by context
- `ErrDoNotCache` for results that must not be stored
- Expired-entry cleanup and `FaultCounts` for failures the cache absorbed
- Type-checked params and result types through generics

## Getting started

### Dependencies

Go 1.26 or later.

### Installing

`go get github.com/rohfle/cachefunk@latest`

### Example

This example caches in memory and needs no database or cgo. The params and result must be encodable
by the configured codecs; both default to JSON.

```go
package main

import (
	"context"
	"fmt"

	"github.com/rohfle/cachefunk"
)

type HelloWorldParams struct {
	Name string
}

func main() {
	cache := cachefunk.CacheFunk{
		Config:  cachefunk.Config{cachefunk.DefaultKey: {TTL: 3600}},
		Storage: cachefunk.NewInMemoryStorage(),
	}

	// helloWorldRaw is the resolver: the function whose results are cached
	helloWorldRaw := func(ignoreCache bool, params *HelloWorldParams) (string, error) {
		return "Hello " + params.Name, nil
	}

	// Wrap the function; ignoreCache is passed to the resolver
	HelloWorld := cachefunk.WrapWithIgnore(&cache, "hello", helloWorldRaw)

	// The first call runs the function, the second returns the cached result
	value, err := HelloWorld(false, &HelloWorldParams{Name: "bob"})
	fmt.Println("First call:", value, err)
	value, err = HelloWorld(false, &HelloWorldParams{Name: "bob"})
	fmt.Println("Second call:", value, err)

	count, _ := cache.EntryCount(context.Background())
	fmt.Println("Entries:", count)
}
```

## Storage backends

`CacheFunk` stores entries through a `CacheStorage`. Three implementations are included:

- **`InMemoryStorage`** keeps entries in process memory, so they are lost when the program exits. Use
  it for tests and examples.
- **`DiskStorage`** keeps each entry in its own file under a directory per key, with the entry path
  derived from params.
- **`GORMStorage`** keeps entries in a database table through [GORM](https://gorm.io). Tables are
  automigrated on first use.

Wiring a storage into CacheFunk is the same for every backend (imports omitted):

```go
db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
if err != nil {
	panic("failed to connect database")
}
storage, err := cachefunk.NewGORMStorage(db)
if err != nil {
	panic("failed to set up cache storage")
}
cache := cachefunk.CacheFunk{
	Config:  cachefunk.Config{cachefunk.DefaultKey: {TTL: 3600}},
	Storage: storage,
}
```

GORMStorage needs cgo for the SQLite driver. Only SQLite is tested; PostgreSQL and other GORM
dialects are untested.

Every storage method takes a `context.Context`. Implementations must check it on entry and never
retain it. They honour cancellation where the backend allows, returning `ctx.Err()` or an error
wrapping it once a call is cancelled.
[`storagetest`](https://pkg.go.dev/github.com/rohfle/cachefunk/storagetest) runs the storage
contract against a supplied backend, including calls made with an already-cancelled context.

## API

Each function takes a cache and key, and either wraps a resolver or makes one call.

| Function           | Returns                                         | Use when                                                |
|--------------------|-------------------------------------------------|---------------------------------------------------------|
| `Wrap`             | `func(Params) (Result, error)`                  | Resolver needs no context or per-call override          |
| `WrapWithIgnore`   | `func(bool, Params) (Result, error)`            | Resolver takes `ignoreCache` per call                   |
| `WrapWithContext`  | `func(context.Context, Params) (Result, error)` | Resolver takes a context; see `SetIgnoreCacheInContext` |
| `Cache`            | `(Result, error)`                               | One call site; wrapping adds nothing                    |
| `CacheWithIgnore`  | `(Result, error)`                               | One call with `ignoreCache`                             |
| `CacheWithContext` | `(Result, error)`                               | One call with a context                                 |

`CacheFunk` also has `Get`, `GetLazy` and `Set` (each taking a `context.Context`) for managing
entries directly. `EntryCount`, `ExpiredEntryCount`, `Cleanup` and `Clear` take a context too, and
`FaultCounts` reports the failures the cache absorbed. See the
[package documentation](https://pkg.go.dev/github.com/rohfle/cachefunk) for every exported name.

## Config

`Config` is a `map[string]*KeyConfig` keyed by cache key, and can be loaded directly from a
JSON config file:

```json
{
	"default": {"ttl": 3600},
	"hello": {
		"ttl": 60,
		"ttl_jitter": 10,
		"fallback_to_expired": true,
		"param_codec": "json+base64",
		"body_codec": "string",
		"body_compression": "none"
	}
}
```

| Key                   | Values                    | Meaning                                                                       |
|-----------------------|---------------------------|-------------------------------------------------------------------------------|
| `ttl`                 | seconds, required         | Lifetime of an entry. `0` expires immediately, a negative value never expires |
| `ttl_jitter`          | seconds, default 0        | Random delay added to an entry's timestamp                                    |
| `fallback_to_expired` | bool, default false       | A failed retrieval returns the expired entry instead of the error             |
| `param_codec`         | `"json"`, `"json+base64"` | How params are serialised to identify an entry                                |
| `body_codec`          | `"json"`, `"string"`      | How cached results are encoded                                                |
| `body_compression`    | `"zstd"`, `"none"`        | How encoded results are compressed                                            |

The reserved key `cachefunk.DefaultKey` (`"default"`) holds settings for every cache key that has
no entry of its own. When a `DefaultKey` entry is not present, keys with no entry fall back to the
global `cachefunk.DefaultKeyConfig` (TTL 3600, jitter 300).

Within an entry, omitted codec and compression fields fall back to `JSONParams`, `JSONCodec` and
`ZstdCompression`. `ttl` and `ttl_jitter` do not fall back: an omitted field is 0. A TTL of 0
expires entries immediately; a jitter of 0 adds no delay.

Params are serialised arguments that identify an entry. The database storage indexes a sha256 of
params rather than the params themselves, as a PostgreSQL index entry is limited to about 2704
bytes. Params are still stored to confirm a match and for debugging.

`Cleanup` and `ExpiredEntryCount` iterate the keys that have entries in storage, and look each up
in `Config` (see `CacheStorage.Keys`). A key with no entry of its own falls back to `DefaultKey` or
`DefaultKeyConfig`, so its entries are counted and cleaned up like the rest.

`Cleanup(ctx, forceCleanupExpired)` skips keys that never expire. Unless `forceCleanupExpired` is
true, it also skips keys with `fallback_to_expired` set: their expired entries can still serve a
failed retrieval.

## Concurrent calls

Callers that miss on the same key and params share one resolver call, and each receives its result.

- **Context**: the resolver runs detached from the initiating caller's cancellation and deadline,
  so one caller giving up does not fail the rest. That caller receives its own context error,
  unless the result is already available.
- **Storage bounds**: reads and writes made on everyone's behalf are bounded by `StorageTimeout`
  (5 seconds by default). A caller's own read is bounded the same way when its context has no
  deadline. A timeout is a warning and a fault count, never a failed call.
- **Cancelled before starting**: a call with an already-cancelled context does not start a resolver,
  but a live cache hit is still served. On a miss it waits for that read, bounded by
  `StorageTimeout`, then returns its context error.
- **ignoreCache**: skips reading the cache and joins a resolver call already running. The result is
  still stored.
- **Panics**: a panic in the resolver becomes an error for every waiting caller. Nothing is stored,
  and the next call resolves again.
- **Nested calls**: a resolver that calls the cache for its own key and params with the context it
  was given gets `ErrReentrantCall` instead of deadlocking. Calls made through `Wrap` or
  `WrapWithIgnore` have no context, so they do not get the error and deadlock.

## Failure handling

Storage failures never fail a cached call. When storage cannot be read or decoded, the cache prints
a warning (see `SetWarningLog`) and the call resolves as if the cache were empty. When a write
fails, the resolved value is returned uncached and the next call resolves it again. A full disk or
an unavailable database therefore costs caching, not service.

A value that cannot be serialised is the exception. That failure is deterministic, so the call
returns an error instead of a warning. `CacheFunk.Set` returns both kinds of error to callers that
manage entries directly.

`FaultCounts` counts the failures the cache absorbed (storage reads, decodes, writes and
serialisations) on the `CacheFunk` value, so alerting does not depend on log volume. The counts are
safe to read while the cache is in use.

Warnings are throttled per format string: each is printed at most once per `DefaultWarningInterval`,
which is one minute by default. The next print reports how many were suppressed, so a cache that
cannot be written to warns once a minute rather than once per call. `SetWarningInterval(0)` prints
every warning.

## How expiry is decided

An entry stores the time it was written, not a fixed expiry time. Expiry is derived from that
timestamp and the key's current TTL on every read, so changing a TTL also applies to entries that
already exist.

Storage is given a cutoff time (`expireBeforeTime`) rather than a TTL: an entry is expired when its
timestamp is before the cutoff. `GetExpireTime` turns the TTL into that cutoff. Use
`TTLEntryNeverExpires` to keep entries forever, or `TTLEntryImmediatelyExpires` to bypass the cache.

| TTL                                       | Cutoff                | Effect                                               |
|-------------------------------------------|-----------------------|------------------------------------------------------|
| positive, up to `TTLMax` (10 years)       | `now - TTL`           | Expires once older than the TTL                      |
| `0` (`TTLEntryImmediatelyExpires`)        | `MaxDate`, 9999-01-01 | Always expired: every timestamp is before the cutoff |
| negative (`TTLEntryNeverExpires` is `-1`) | `MinDate`, 1970-01-01 | Never expires: no timestamp is before the cutoff     |
| above `TTLMax`                            | `MinDate`, 1970-01-01 | Never expires                                        |

A TTL above `TTLMax` is treated as forever by design. Ten years is already effectively infinite for
a cache. The cap keeps cutoff arithmetic inside the supported date range and avoids overflow from
absurd TTLs.

With a 60-second TTL at 12:00:00, the cutoff is 11:59:00. An entry written at 11:58:30 is expired;
one written at 11:59:30 is not. An entry written exactly at the cutoff is not expired: the
comparison is strictly before.

`MinDate` and `MaxDate` are the ends of the timestamp range cachefunk uses, chosen to stay within
what file systems and databases represent. Cachefunk writes only clock-derived timestamps, which
sit between them. Encoding "never" and "always" as cutoffs keeps the storage contract to one time
comparison, with no separate flags in any backend.

If the clock is set before `MinDate + TTL` (before 1980 at the 10-year cap), `now - TTL` falls
before `MinDate`. The cutoff is then clamped to `MinDate`, so entries never expire.

Jitter shifts the stored timestamp later by a random amount, which spreads out expiry so entries
written together do not all expire together. The shift is less than the jitter value and may be
zero. The jitter value is `TTLJitter` capped at the TTL and at one year, and 0 for non-positive
TTLs. Because jitter is applied when the entry is written, changing the setting does not move
existing timestamps.

An expired entry is not deleted by a read, so with `fallback_to_expired` set a failed resolver call
can still return it. `Cleanup` removes it.

A TTL of `TTLEntryImmediatelyExpires` also makes `Set` store nothing, unless the key sets
`fallback_to_expired`.

## Version history

See [CHANGELOG.md](CHANGELOG.md).

## License

© Rohan Fletcher 2025

This project is licensed under the MIT License; see the [LICENSE](LICENSE) file for details.
