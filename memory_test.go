package cachefunk

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestInMemoryStorage(t *testing.T) {
	config := Config{}

	storage := NewInMemoryStorage()

	cache := &CacheFunk{
		Config:  config,
		Storage: storage,
	}

	runTestWrapWithStringResult(t, cache)
	cache.Clear(context.Background())
	runTestWrapWithContextAndStringResult(t, cache)
	cache.Clear(context.Background())
	runTestWrapWithIgnoreAndObjectResult(t, cache)
	cache.Clear(context.Background())
	runTestWrapWithContextAndObjectResult(t, cache)
	cache.Clear(context.Background())
	runTestWrapAndObjectResult(t, cache)
	cache.Clear(context.Background())
	runTestCacheFuncErrorsReturned(t, cache)
	cache.Clear(context.Background())
	runTestCacheFuncWithContextErrorsReturned(t, cache)
	cache.Clear(context.Background())
	expireAllEntries := func() {
		for _, paramsMap := range storage.store {
			for _, value := range paramsMap {
				value.Timestamp = time.Now().UTC().Add(-3600 * time.Second)
			}
		}
	}
	runTestCacheFuncTTL(t, cache, expireAllEntries)
	cache.Clear(context.Background())
	runTestCacheFallBackToExpired(t, cache, expireAllEntries)
	cache.Clear(context.Background())
	runTestCacheFallBackToExpiredWithContext(t, cache, expireAllEntries)
	cache.Clear(context.Background())
	runTestCacheMismatchCompressionType(t, cache, expireAllEntries)
	cache.Clear(context.Background())
	runTestCleanupUnconfiguredKey(t, cache, expireAllEntries)
	cache.Clear(context.Background())
	runTestCacheCompressedPayload(t, cache)
	cache.Clear(context.Background())
	runTestSingleflightCoalesces(t, cache)
	cache.Clear(context.Background())
	runTestSingleflightPanicIsReturnedAsError(t, cache)
	cache.Clear(context.Background())
	runTestSingleflightDoneContext(t, cache)
	cache.Clear(context.Background())
	runTestSingleflightCallerGivesUp(t, cache)
	cache.Clear(context.Background())
	runTestSingleflightReentrantCall(t, cache)
	cache.Clear(context.Background())
	runTestSingleflightNestedCalls(t, cache)
	cache.Clear(context.Background())
	runTestCacheErrDoNotCache(t, cache)
	cache.Clear(context.Background())
}

func ExampleInMemoryStorage() {
	type HelloWorldParams struct {
		Name string
	}

	helloWorld := func(ignoreCache bool, params *HelloWorldParams) (string, error) {
		return "Hello " + params.Name, nil
	}

	config := Config{}

	cache := &CacheFunk{
		Config:  config,
		Storage: NewInMemoryStorage(),
	}

	HelloWorld := WrapWithIgnore(cache, "hello", helloWorld)
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
