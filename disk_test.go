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

	storage, err := NewDiskStorage(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("error while creating disk storage: %s", err)
	}

	cache := &CacheFunk{
		Config:  config,
		Storage: storage,
	}

	runTestWrapWithStringResult(t, cache)
	cache.Clear()
	runTestWrapWithContextAndStringResult(t, cache)
	cache.Clear()
	runTestWrapWithIgnoreAndObjectResult(t, cache)
	cache.Clear()
	runTestWrapWithContextAndObjectResult(t, cache)
	cache.Clear()
	runTestWrapAndObjectResult(t, cache)
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

func TestBadCacheKeyPaths(t *testing.T) {
	config := &Config{}

	tempDir := t.TempDir()

	storage, err := NewDiskStorage(tempDir, nil)
	if err != nil {
		t.Fatalf("error while creating disk storage: %s", err)
	}

	cache := &CacheFunk{
		Config:  config,
		Storage: storage,
	}

	badKeys := []string{
		"",
		"../foo",
		"../",
		".",
		"../../...../.../././././././././.",
	}

	original := "here is a value"
	value := original

	for _, key := range badKeys {
		err := cache.Set(key, DefaultKeyConfig, "", value)
		if err == nil {
			t.Errorf("expected error with bad key %q, but got nil", key)
			continue
		}

		err = cache.Get(key, DefaultKeyConfig, "", &value)
		if err == nil {
			t.Errorf("expected error with bad key %q, but got nil", key)
			continue
		}
	}

	err = cache.Clear()
	if err != nil {
		t.Fatalf("error while clearing cache: %s", err)
	}

	goodKeys := []string{
		"key",
		"a/b/c/d/e",
	}

	value = original

	for _, key := range goodKeys {
		err := cache.Set(key, DefaultKeyConfig, "", value)
		if err != nil {
			t.Errorf("expected no error with good key %s, but got %s", key, err)
		}
		err = cache.Get(key, DefaultKeyConfig, "", &value)
		if err != nil {
			t.Errorf("expected no error with good key %s, but got %s", key, err)
		}

		if value != original {
			t.Errorf("expected %q got %q with good key %q", original, value, key)
			continue
		}
	}

	err = cache.Clear()
	if err != nil {
		t.Fatalf("error while clearing cache: %s", err)
	}

}

func ExampleDiskStorage() {
	type HelloWorldParams struct {
		Name string
	}

	helloWorld := func(ignoreCache bool, params *HelloWorldParams) (string, error) {
		return "Hello " + params.Name, nil
	}

	config := &Config{}
	storage, err := NewDiskStorage("/path/to/cache", DefaultDiskStoragePather)
	if err != nil {
		fmt.Printf("Error while creating disk storage: %s\n", err)
		return
	}
	cache := &CacheFunk{
		Config:  config,
		Storage: storage,
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
