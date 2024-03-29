package cachefunk_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/rohfle/cachefunk"
)

func TestInMemoryCache(t *testing.T) {
	cache := cachefunk.NewInMemoryCache()

	runTestWrapString(t, cache)
	cache.Clear()
	runTestWrapStringWithContext(t, cache)
	cache.Clear()
	runTestWrapObject(t, cache)
	cache.Clear()
	runTestWrapObjectWithContext(t, cache)
	cache.Clear()
	runTestCacheFuncErrorsReturned(t, cache)
	cache.Clear()
	runTestCacheFuncWithContextErrorsReturned(t, cache)
	cache.Clear()
	expireAllEntries := func() {
		for _, value := range cache.Store {
			value.Timestamp = time.Time{}
		}
	}
	runTestCacheFuncTTL(t, cache, expireAllEntries)
}

func ExampleInMemoryCache() {
	type HelloWorldParams struct {
		Name string
	}

	helloWorld := func(ignoreCache bool, params *HelloWorldParams) (string, error) {
		return "Hello " + params.Name, nil
	}

	cache := cachefunk.NewInMemoryCache()

	HelloWorld := cachefunk.WrapString(cache, "hello", helloWorld)
	params := &HelloWorldParams{
		Name: "bob",
	}

	// First call will get value from wrapped function
	value, err := HelloWorld(false, params)
	fmt.Println("First call:", value, err)
	// Second call will get value from cache
	value, err = HelloWorld(false, params)
	fmt.Println("Second call:", value, err)
}
