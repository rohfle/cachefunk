package cachefunk_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rohfle/cachefunk"
)

func TestInMemoryCache(t *testing.T) {
	cache := cachefunk.NewInMemoryCache()

	runTestWrapString(t, cache)
	cache.Clear()
	runTestWrap(t, cache)
	cache.Clear()
	runTestCacheFuncErrorsReturned(t, cache)
	cache.Clear()
	expireAllEntries := func(includeForever bool) {
		for _, value := range cache.Store {
			if includeForever || value.ExpiresAt != nil {
				t := time.Now().UTC()
				value.ExpiresAt = &t
			}
		}
	}
	runTestCacheFuncTTL(t, cache, expireAllEntries)
}

func ExampleInMemoryCache() {
	type HelloWorldParams struct {
		Name string
	}

	helloWorld := func(ctx *context.Context, ignoreCache bool, params *HelloWorldParams) (string, error) {
		return "Hello " + params.Name, nil
	}

	cache := cachefunk.NewInMemoryCache()
	HelloWorld := cachefunk.WrapString(helloWorld, cache, cachefunk.Config{
		Key: "hello",
		TTL: 3600,
	})

	ctx := context.TODO()

	// First call will retrieve value from given function
	value, err := HelloWorld(&ctx, false, &HelloWorldParams{
		Name: "bob",
	})
	fmt.Println(value, err)
	// Second call will retrieve value from cache
	value, err = HelloWorld(&ctx, false, &HelloWorldParams{
		Name: "bob",
	})
	fmt.Println("Result:", value, err)
}
