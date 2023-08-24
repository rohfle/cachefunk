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

func TestDiskCache(t *testing.T) {
	cache := cachefunk.NewDiskCache(t.TempDir())

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
		cache.IterateFiles(cache.BasePath, func(parent string, file fs.DirEntry) {
			if _, err := file.Info(); err != nil {
				return
			}
			os.Chtimes(filepath.Join(parent, file.Name()), time.Time{}, time.Unix(0, 0))
		})
	}
	runTestCacheFuncTTL(t, cache, expireAllEntries)
}

func ExampleDiskCache() {
	type HelloWorldParams struct {
		Name string
	}

	helloWorld := func(ignoreCache bool, params *HelloWorldParams) (string, error) {
		return "Hello " + params.Name, nil
	}

	cache := cachefunk.NewDiskCache("/path/to/cache")

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
