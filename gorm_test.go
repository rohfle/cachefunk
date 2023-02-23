package cachefunk_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/rohfle/cachefunk"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type HelloWorldParams struct {
	Name string
	Age  int64
}

func TestGORMCache(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal("failed to connect database")
	}

	cache := cachefunk.NewGORMCache(db)
	runTestWrapString(t, cache)
	cache.Clear()
	runTestWrapStringWithContext(t, cache)
	cache.Clear()
	runTestWrap(t, cache)
	cache.Clear()
	runTestWrapWithContext(t, cache)
	cache.Clear()
	runTestCacheFuncErrorsReturned(t, cache)
	cache.Clear()
	runTestCacheFuncWithContextErrorsReturned(t, cache)
	cache.Clear()
	expireAllEntries := func(includeForever bool) {
		cache.DB.Model(cachefunk.CacheEntry{}).Where("expires_at IS NOT NULL").Update("expires_at", time.Now().UTC())
	}
	runTestCacheFuncTTL(t, cache, expireAllEntries)
	cache.Clear()
	runTestCachePoisoning(t, cache)
}

func ExampleGORMCache() {
	type HelloWorldParams struct {
		Name string
	}

	helloWorld := func(ignoreCache bool, params *HelloWorldParams) (string, error) {
		return "Hello " + params.Name, nil
	}

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		panic("failed to connect database")
	}

	cache := cachefunk.NewGORMCache(db)

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
