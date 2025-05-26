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
- Supports custom marshal / unmarshal: json, msgpack, string
- Supports compression: zstd, gzip, brotli
- Configurable TTL and TTL jitter
- Cleanup function for periodic removal of expired entries
- Uses go generics, in IDE type checked parameters and result
- Cache can be ignored, either by boolean or by ctx key

## Getting Started

### Dependencies

* go version that supports generics (tested on v1.19)

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
				// BodyCompression: cachefunk.ZstdCompression,
				// BodyCodec: cachefunk.JSONCodec,
				// ParamsCodec: cachefunk.JSONCodec,
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
	HelloWorld := cachefunk.Wrap(cache, "hello", helloWorldRaw)

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
- WrapWithContext
- Cache
- CacheWithContext

## Notes about timestamps

- Timestamps store the time when the cached item was saved with jitter applied
- It is easier to apply jitter to timestamps at save even though jitter TTL might change
- The expire time is not stored because cache config TTL might change on subsequent runs
- Cache items must be able to immediately expire and never expire, regardless of stored timestamp
- Cache get calls should not expire items - only return no match in case subsequent retrieve fails
- Expire time of 1970-01-01 00:00:00 is used for expire immediately
- Expire time of 9999-01-01 00:00:00 is used for never expire

## Version History

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

© Rohan Fletcher 2023

This project is licensed under the MIT License - see the LICENSE file for details
