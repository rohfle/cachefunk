package cachefunk_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rohfle/cachefunk"
)

func TestDiskStorage(t *testing.T) {
	config := &cachefunk.Config{}

	storage := cachefunk.NewDiskStorage(t.TempDir(), nil)
	cache := &cachefunk.CacheFunk{
		Config:       config,
		Storage:      storage,
		IgnoreCtxKey: cachefunk.DefaultIgnoreCacheCtxKey,
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

	config := &cachefunk.Config{}
	storage := cachefunk.NewDiskStorage("/path/to/cache", cachefunk.DefaultDiskStoragePather)
	cache := &cachefunk.CacheFunk{
		Config:  config,
		Storage: storage,
	}

	HelloWorld := cachefunk.Wrap(cache, "hello", helloWorld)
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
