# cachefunk

Use wrapper functions to cache function output in golang.

[![Go Report Card](https://goreportcard.com/badge/github.com/rohfle/cachefunk)](https://goreportcard.com/report/github.com/rohfle/cachefunk)
[![Test](https://github.com/rohfle/cachefunk/actions/workflows/test.yml/badge.svg)](https://github.com/rohfle/cachefunk/actions/workflows/test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/rohfle/cachefunk.svg)](https://pkg.go.dev/github.com/rohfle/cachefunk)

## Features

- Currently supported cache adapters:
	- any GORM-supported database
	- in-memory caching
	- files on disk
- Custom marshal / unmarshal: json, msgpack, string
- Compression: zstd, gzip, brotli
- Configurable TTL and TTL jitter
- Configurable fallback to expired when downstream fails
- Cleanup function for periodic removal of expired entries
- Uses go generics, in IDE type checked parameters and result
- Cache can be ignored, either by boolean or by ctx key
- Use `ErrDoNotCache` from target function to return result without caching it

## Getting Started

### Dependencies

* go version that supports generics (tested v1.23 and v1.24)

### Installing

`go get -u github.com/rohfle/cachefunk`

### Example

```golang

import (
	"fmt"
	"testing"
	"time"

	"github.com/rohfle/cachefunk"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)


func main() {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		panic("failed to connect database")
	}

	config := cachefunk.Config{
		Configs: {
			"hello": {
				TTL: 3600,
				// TTLJitter: 0,
				// FallbackToExpired: false,
				// BodyCompression: cachefunk.ZstdCompression,
				// BodyCodec: cachefunk.JSONCodec,
				// ParamsCodec: cachefunk.JSONParams,
			}
		}
	}
	storage := cachefunk.NewGORMStorage(db)
	cache := cachefunk.CacheFunk{
		Config: config,
		Storage: storage,
	}

	// ignoreCache is passed through to the target function for nested caching calls
	// All other arguments are passed in as a struct (HelloWorldParams)
	// The params argument and the return type must be serializable by the codec Marshal
	type HelloWorldParams struct {
		Name string
	}

	helloWorldRaw := func(ignoreCache bool, params *HelloWorldParams) (string, error) {
		return "Hello " + params.Name, nil
	}

	// Wrap the function
	HelloWorld := cachefunk.WrapWithIgnore(cache, "hello", helloWorldRaw)

	// First call will get value from wrapped function
	value, err := HelloWorld(false, &HelloWorldParams{
		Name: "bob",
	})
	fmt.Println("First call:", value, err)

	// Second call will get value from cache
	value, err = HelloWorld(false, &HelloWorldParams{
		Name: "bob",
	})
	fmt.Println("Second call:", value, err)
}
```

## API

- Wrap
- WrapWithIgnore
- WrapWithContext
- Cache
- CacheWithIgnore
- CacheWithContext

## Notes about timestamps

- Timestamps store the time when the cached item was saved with jitter applied
- It is easier to apply jitter to timestamps at save even though jitter TTL might change
- The expire time is not stored because cache config TTL might change on subsequent runs
- Cache items must be able to immediately expire and never expire, regardless of stored timestamp
- Cache get calls should not expire items - only return no match in case subsequent retrieve fails
- Any entry with a timestamp before the expire time is said to have expired
- A `TTL` of `0` or `TTLEntryImmediatelyExpires` is used for immediate expiry (`MaxTime`, 9999-01-01)
- A `TTL` of `-1` or `TTLEntryNeverExpires` is used for no expiry  (`MinTime`, 1970-01-01)

## Version History

* 0.4.1
	* Cache TTLEntryImmediatelyExpires entries when FallbackToExpired is set
	* Added lazy load of existing expired entries
	* Fallback to Expired
	* Changed Wrap to WrapWithIgnore, Cache to CacheWithIgnore
	* Removed ability set ignore cache ctx key
	* Ensure DiskStorage file paths are within the cache
* 0.4.0
	* Complete rewrite
	* Compression and Codec methods are now per config key
	* Removed string / object specific functions, now unified type handling
	* Added zstd, brotli, msgpack support
	* Added warning log, DisableWarnings and EnableWarnings function
* 0.3.0
	* Added disk cache
	* Changed from storing expire time to timestamp when entry was cached
	* Added gzip compression
	* Changed CacheResult to CacheObject, CacheWithContext to CacheObjectWithContext
	* Moved TTL configuration to cache initialization function
	* Removed TTL value for store indefinitely
	* Messed around with git version tags to try to erase history
* 0.2.0
	* Created CacheResult, CacheString, CacheWithContext, CacheStringWithContext functions
* 0.1.0
    * Initial release

## License

© Rohan Fletcher 2025

This project is licensed under the MIT License - see the LICENSE file for details
