package cachefunk

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestDiskStorage(t *testing.T) {
	config := Config{}

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
	config := Config{}

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

// TestDiskStorageCleanupPathEscape is a deterministic regression test: Cleanup
// and ExpiredEntryCount must not touch files outside BasePath. They used to
// join the bare key onto BasePath with no validation, so a Config key such as
// "../sibling" made CacheFunk.Cleanup (which runs automatically for every
// configured key) delete arbitrary files on disk.
func TestDiskStorageCleanupPathEscape(t *testing.T) {
	parentDir := t.TempDir()
	baseDir := filepath.Join(parentDir, "cache")

	storage, err := NewDiskStorage(baseDir, nil)
	if err != nil {
		t.Fatalf("error while creating disk storage: %s", err)
	}

	siblingDir := filepath.Join(parentDir, "sibling")
	if err := os.MkdirAll(siblingDir, 0755); err != nil {
		t.Fatalf("failed to create sibling directory: %s", err)
	}
	victim := filepath.Join(siblingDir, "victim.txt")
	if err := os.WriteFile(victim, []byte("please keep me"), 0644); err != nil {
		t.Fatalf("failed to write victim file: %s", err)
	}
	// Age the file so Cleanup would consider it expired if it reached it.
	old := time.Now().UTC().Add(-24 * time.Hour)
	if err := os.Chtimes(victim, old, old); err != nil {
		t.Fatalf("failed to age victim file: %s", err)
	}

	expireBeforeTime := time.Now().UTC()

	// "../sibling" is caught by the blank/dot key rule; the second key only
	// escapes through the cache root, so it exercises filepath.IsLocal.
	for _, key := range []string{"../sibling", "sub/../../sibling"} {
		if err := storage.Cleanup(key, DefaultKeyConfig, expireBeforeTime); err == nil {
			t.Errorf("expected Cleanup with key %q to be rejected, but got nil error", key)
		}
		if _, err := storage.ExpiredEntryCount(key, DefaultKeyConfig, expireBeforeTime); err == nil {
			t.Errorf("expected ExpiredEntryCount with key %q to be rejected, but got nil error", key)
		}
		if err := storage.Set(key, DefaultKeyConfig, "", []byte("value"), expireBeforeTime); err == nil {
			t.Errorf("expected Set with key %q to be rejected, but got nil error", key)
		}
		if _, err := os.Stat(victim); err != nil {
			t.Errorf("file outside the cache root was affected by key %q: %s", key, err)
		}
	}
}

// TestDiskStorageConcurrentSetGetNoTornReads is a probabilistic regression
// test for atomic Set. It protects the invariant that a concurrent Get never
// observes a partially written entry: before Set wrote to a dot-prefixed temp
// file and renamed it into place, os.WriteFile truncated the target first, so
// readers could see a short or empty file and return it with a nil error -
// indistinguishable from a legitimate cached value. Timing dependent, so a
// torn read is not guaranteed on every run, but a 1 MiB value rewritten a few
// hundred times while four goroutines read makes it very likely.
func TestDiskStorageConcurrentSetGetNoTornReads(t *testing.T) {
	config := &KeyConfig{BodyCompression: NoCompression, BodyCodec: StringCodec}

	storage, err := NewDiskStorage(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("error while creating disk storage: %s", err)
	}

	const key = "concurrent"
	want := bytes.Repeat([]byte("0123456789abcdef"), 1<<16) // 1 MiB

	// Prime the entry so readers never race against the very first write.
	if err := storage.Set(key, config, "", want, time.Now().UTC()); err != nil {
		t.Fatalf("error while priming cache: %s", err)
	}

	notExpired := time.Unix(0, 0)
	stop := make(chan struct{})
	results := make(chan error, 8)

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				got, err := storage.Get(key, config, "", notExpired)
				if err != nil {
					results <- fmt.Errorf("unexpected Get error: %w", err)
					return
				}
				if !bytes.Equal(got, want) {
					results <- fmt.Errorf("torn read: got %d bytes, want %d", len(got), len(want))
					return
				}
			}
		}()
	}

	for range 300 {
		if err := storage.Set(key, config, "", want, time.Now().UTC()); err != nil {
			t.Fatalf("Set failed: %s", err)
		}
	}

	close(stop)
	wg.Wait()
	close(results)
	for err := range results {
		t.Error(err)
	}
}

func ExampleDiskStorage() {
	type HelloWorldParams struct {
		Name string
	}

	helloWorld := func(ignoreCache bool, params *HelloWorldParams) (string, error) {
		return "Hello " + params.Name, nil
	}

	config := Config{}
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
