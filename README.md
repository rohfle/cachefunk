# cachefunk

Cache the results of function calls in Go using different storage backends. Supports TTL and compression.

[![Test](https://github.com/rohfle/cachefunk/actions/workflows/test.yml/badge.svg)](https://github.com/rohfle/cachefunk/actions/workflows/test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/rohfle/cachefunk.svg)](https://pkg.go.dev/github.com/rohfle/cachefunk)

## Features

- Three storage backends: in memory, files on disk, or a GORM database (PostgreSQL or SQLite)
- A TTL per key, with optional jitter
- Fall back to an expired entry when the resolver fails
- Results as JSON or as raw strings, compressed with zstd or not
- One resolver call per key and params, however many callers miss at once
- Cache bypass per call: by boolean or by context
- `ErrDoNotCache` lets the resolver return a result without caching it
- `Cleanup` removes expired entries; `FaultCounts` reports the failures the cache absorbed
- Type-checked params and results through generics

## Getting started

### Dependencies

Go 1.26 or later.

### Installing

`go get github.com/rohfle/cachefunk@latest`

### Example

This example caches in memory, it does not need a database or cgo. The params and result
must be encodable by the configured codec; the default is JSON.

```go
package main

import (
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

	// Wrap the function. ignoreCache is passed through to it, so a nested call
	// can bypass the cache
	HelloWorld := cachefunk.WrapWithIgnore(&cache, "hello", helloWorldRaw)

	// The first call runs the function, the second returns the cached result
	value, err := HelloWorld(false, &HelloWorldParams{Name: "bob"})
	fmt.Println("First call:", value, err)
	value, err = HelloWorld(false, &HelloWorldParams{Name: "bob"})
	fmt.Println("Second call:", value, err)

	count, _ := cache.EntryCount()
	fmt.Println("Entries:", count)
}
```

## Storage backends

`CacheFunk` stores entries through a `CacheStorage`. Three implementations are included:

- **`InMemoryStorage`** keeps entries in process memory, so they are lost when the program exits. Used for testing and quick examples.
- **`DiskStorage`** keeps each entry to its own file under a directory tree, Its path and filename is based on params and key.
- **`GORMStorage`** keeps entries in a database table through [GORM](https://gorm.io). Tables are automigrated on first use.

Each storage is used the same way:

```go
db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
if err != nil {
	panic("failed to connect database")
}
storage, err := cachefunk.NewGORMStorage(db)
if err != nil {
	panic("failed to setup cache storage")
}
cache := cachefunk.CacheFunk{
	Config:  cachefunk.Config{cachefunk.DefaultKey: {TTL: 3600}},
	// storage is set here
	Storage: storage,
}
```

Note that GORMStorage needs cgo for the SQLite driver. PostgreSQL and SQLite are tested dialects; other GORM databases
are untested.

## API

Each function takes the cache, a key and the params, and either returns a wrapped function or makes
one call.

| Function | Returns | Use when |
|---|---|---|
| `Wrap` | `func(Params) (Result, error)` | The target function needs neither a context nor a per-call override |
| `WrapWithIgnore` | `func(bool, Params) (Result, error)` | The target function decides per call whether to skip reading the cache |
| `WrapWithContext` | `func(context.Context, Params) (Result, error)` | The target function takes a context, or the context is marked by `SetIgnoreCacheInContext` |
| `Cache` | `(Result, error)` | There is a single call site, so wrapping adds nothing |
| `CacheWithIgnore` | `(Result, error)` | A single call that passes `ignoreCache` |
| `CacheWithContext` | `(Result, error)` | A single call that passes a context |

`CacheFunk` also has `Get`, `GetLazy` and `Set` for managing entries directly, `EntryCount`,
`ExpiredEntryCount`, `Cleanup` and `Clear`, and `FaultCounts` for failures the cache absorbed. See
the [package documentation](https://pkg.go.dev/github.com/rohfle/cachefunk) for every exported name.

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

| Key | Values | Meaning |
|---|---|---|
| `ttl` | seconds, required | Lifetime of an entry. `0` expires immediately, a negative value never expires |
| `ttl_jitter` | seconds, default 0 | Random delay added to an entry's timestamp, capped at the TTL |
| `fallback_to_expired` | bool, default false | A failed retrieve returns the expired entry instead of the error |
| `param_codec` | `"json"`, `"json+base64"` | How params are serialised to identify an entry |
| `body_codec` | `"json"`, `"string"` | How cached results are encoded |
| `body_compression` | `"zstd"`, `"none"` | How encoded results are compressed |

The reserved key `cachefunk.DefaultKey` (`"default"`) holds settings for every cache key that has
no entry of its own. When a `DefaultKey` entry is not present, keys with no entry fall back to the
global `cachefunk.DefaultKeyConfig`.

Omitted fields fall back to the global defaults: TTL 3600, jitter 300, `JSONParams`, `JSONCodec`
and `ZstdCompression`.

`ttl_jitter` seconds are added to the timestamp of an entry, capped so jitter can never extend an
entry's lifetime past two times its TTL.

Params are serialised arguments that identify an entry. The database storage uses a sha256 of params
for indexing rather than the value, as index entries have limits of about 2.7 KB in PostgreSQL.
The params themselves are still stored to confirm a match and for debugging purposes.

`Cleanup` and `ExpiredEntryCount` walk the keys that have entries in storage, and look each one up in
`Config` (see `CacheStorage.Keys`). A key with no entry of its own falls back to `DefaultKey` or
`DefaultKeyConfig`, so its entries are counted and cleaned up like the rest.

`Cleanup(forceCleanupExpired)` skips keys that never expire. It also skips keys with
`fallback_to_expired` set, because their expired entries can still answer a failed retrieve, unless
`forceCleanupExpired` is true.

## Concurrent calls

Callers that miss on the same key and params share one resolver call, and each receives its result.
Different params make separate calls.

- **Context**: the resolver runs detached from the initiating caller's cancellation and deadline, so
  that caller giving up does not fail the rest. A caller that gives up gets its own context error, or
  the result if it is already available.
- **Cancelled before starting**: the call returns before starting a resolver call, but a live cache
  hit is still served.
- **ignoreCache**: skips reading the cache and joins any resolver call already in flight. The result
  is still stored.
- **Panics**: a panic in the resolver becomes an error for every waiting caller. Nothing is stored,
  and the next call tries again.
- **Nested calls**: a resolver that calls the cache for its own key and params with the context it
  was given gets `ErrReentrantCall` instead of deadlocking. Nested calls made through `Wrap` or
  `WrapWithIgnore` drop the context, do not get the error, and do deadlock.

## Failure handling

A storage failure never fails the call. When storage cannot be read, decoded or written, the cache
prints a warning (see `SetWarningLog`) and the resolver runs as if the cache were empty. A full disk
or an unavailable database therefore costs caching, not service. A value that could not be stored is
still returned to the caller, and the next call resolves it again.

A value that cannot be serialised is the exception: that outcome is deterministic, so the call
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

An entry stores the time it was written, not a fixed expiry time, so a changed TTL applies to
entries that already exist.

- Jitter is applied when the entry is written, because the setting can change between runs. It is
  capped at the TTL, so jitter cannot extend an entry's life past twice its TTL.
- Any entry whose timestamp is before the expire time counts as expired.
- A TTL of `0` or `TTLEntryImmediatelyExpires` expires entries immediately (`MaxDate`, 9999-01-01).
- A TTL of `-1` or `TTLEntryNeverExpires` never expires (`MinDate`, 1970-01-01).
- A cache read does not delete an expired entry. It reports the entry as expired, so a failed
  resolver call can still return the stale value.

## Version history

See [CHANGELOG.md](CHANGELOG.md).

## License

© Rohan Fletcher 2025

This project is licensed under the MIT License - see the LICENSE file for details
