# cachefunk

Use wrapper functions to cache function output in golang.

[![Go Report Card](https://goreportcard.com/badge/github.com/rohfle/cachefunk)](https://goreportcard.com/report/github.com/rohfle/cachefunk)
[![Test](https://github.com/rohfle/cachefunk/actions/workflows/test.yml/badge.svg)](https://github.com/rohfle/cachefunk/actions/workflows/test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/rohfle/cachefunk.svg)](https://pkg.go.dev/github.com/rohfle/cachefunk)

## Features

- Currently supported cache adapters:
	- any GORM-supported database
	- in-memory caching
- Configurable TTL and TTL jitter
- Cleanup function for periodic removal of expired entries
- Uses go generics, in IDE type checked parameters and result
- Can ignore cached values

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
	type HelloWorldParams struct {
		Name string
	}

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		panic("failed to connect database")
	}

	cache := cachefunk.NewGORMCache(db)

    // Define a function
	// ignoreCache is passed through if the function calls other wrapped functions.
	// Note that the only other argument supported currently is params.
	// This params argument can be any value (typically a struct) that can be serialized into JSON
	// WrapString is used to wrap this function, so it must return string or []byte
	helloWorld := func(ignoreCache bool, params *HelloWorldParams) (string, error) {
		return "Hello " + params.Name, nil
	}

    // Wrap the function
	HelloWorld := cachefunk.WrapString(helloWorld, cache, cachefunk.Config{
		Key: "hello",
		TTL: 3600,
	})

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

- WrapString: store the result as []byte
- Wrap: encode as JSON and then store the result as []byte

## Dreams for the Future

- "Unvariadicize" to allow
	- passing through set number of args
	- through wrapper which caches using variadic
	- to wrapped function that takes set number of non-variadic args
- Export wrapped functions at the package level more easily
- Allow generic methods on types


## Version History

* 0.1
    * Initial Release

## License

© Rohan Fletcher 2023

This project is licensed under the MIT License - see the LICENSE file for details
