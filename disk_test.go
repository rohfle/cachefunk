package cachefunk

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiskStorage(t *testing.T) {
	config := &Config{}

	storage := NewDiskStorage(t.TempDir(), nil)
	cache := &CacheFunk{
		Config:       config,
		Storage:      storage,
		IgnoreCtxKey: DefaultIgnoreCacheCtxKey,
	}

	runTestWrapWithStringResult(t, cache)
	cache.Clear()
	runTestWrapWithContextAndStringResult(t, cache)
	cache.Clear()
	runTestWrapWithObjectResult(t, cache)
	cache.Clear()
	runTestWrapWithContextAndObjectResult(t, cache)
	cache.Clear()
	runTestCacheFuncErrorsReturned(t, cache)
	cache.Clear()
	runTestCacheFuncWithContextErrorsReturned(t, cache)
	cache.Clear()
	expireAllEntries := func() {
		storage.IterateFiles(storage.BasePath, func(parent string, file fs.DirEntry) {
			if _, err := file.Info(); err != nil {
				return
			}
			os.Chtimes(filepath.Join(parent, file.Name()), time.Time{}, time.Now().UTC().Add(-3600*time.Second))
		})
	}
	runTestCacheFuncTTL(t, cache, expireAllEntries)
	cache.Clear()
	runTestCacheFallBackToExpired(t, cache, expireAllEntries)
	cache.Clear()
	runTestCacheFallBackToExpiredWithContext(t, cache, expireAllEntries)
	cache.Clear()
	runTestCacheMismatchCompressionType(t, cache, expireAllEntries)
}

func ExampleDiskStorage() {
	type HelloWorldParams struct {
		Name string
	}

	helloWorld := func(ignoreCache bool, params *HelloWorldParams) (string, error) {
		return "Hello " + params.Name, nil
	}

	config := &Config{}
	storage := NewDiskStorage("/path/to/cache", DefaultDiskStoragePather)
	cache := &CacheFunk{
		Config:  config,
		Storage: storage,
	}

	HelloWorld := Wrap(cache, "hello", helloWorld)
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
