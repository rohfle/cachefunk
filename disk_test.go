package cachefunk

import (
	"bytes"
	"context"
	"errors"
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
		storage.iterateFiles(context.Background(), storage.BasePath, func(parent string, file fs.DirEntry) {
			if _, err := file.Info(); err != nil {
				return
			}
			os.Chtimes(filepath.Join(parent, file.Name()), time.Time{}, time.Now().UTC().Add(-3600*time.Second))
		})
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

// TestDiskStorageCleanupRemovesEmptyDirs checks that cleanup does not leave the
// directory chain of a removed entry behind
func TestDiskStorageCleanupRemovesEmptyDirs(t *testing.T) {
	storage, err := NewDiskStorage(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("error while creating disk storage: %s", err)
	}
	config := &KeyConfig{TTL: 1, BodyCodec: StringCodec, BodyCompression: NoCompression}
	expired := time.Now().UTC().Add(-3600 * time.Second)
	for _, params := range []string{"p1", "p2", "p3"} {
		if err := storage.Set(context.Background(), "api", config, params, []byte("value"), expired); err != nil {
			t.Fatalf("error while setting entry: %s", err)
		}
	}

	if err := storage.Cleanup(context.Background(), "api", config, time.Now().UTC()); err != nil {
		t.Fatalf("unexpected error from Cleanup: %s", err)
	}

	var dirs []string
	err = filepath.WalkDir(storage.BasePath, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && path != storage.BasePath {
			dirs = append(dirs, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("error while walking cache: %s", err)
	}
	if len(dirs) != 0 {
		t.Errorf("expected cleanup to remove the empty directories but found %v", dirs)
	}

	keys, err := storage.Keys(context.Background())
	if err != nil {
		t.Fatalf("error while fetching cache keys: %s", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected no keys after cleanup but got %v", keys)
	}
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
		// ".." is rejected anywhere in a key, not only as a path element
		"a..b",
		"12..34",
	}

	original := "here is a value"
	value := original

	for _, key := range badKeys {
		err := cache.Set(context.Background(), key, DefaultKeyConfig, "", value)
		if err == nil {
			t.Errorf("expected error with bad key %q, but got nil", key)
			continue
		}

		err = cache.Get(context.Background(), key, DefaultKeyConfig, "", &value)
		if err == nil {
			t.Errorf("expected error with bad key %q, but got nil", key)
			continue
		}
	}

	err = cache.Clear(context.Background())
	if err != nil {
		t.Fatalf("error while clearing cache: %s", err)
	}

	goodKeys := []string{
		"key",
		"a/b/c/d/e",
	}

	value = original

	for _, key := range goodKeys {
		err := cache.Set(context.Background(), key, DefaultKeyConfig, "", value)
		if err != nil {
			t.Errorf("expected no error with good key %s, but got %s", key, err)
		}
		err = cache.Get(context.Background(), key, DefaultKeyConfig, "", &value)
		if err != nil {
			t.Errorf("expected no error with good key %s, but got %s", key, err)
		}

		if value != original {
			t.Errorf("expected %q got %q with good key %q", original, value, key)
			continue
		}
	}

	err = cache.Clear(context.Background())
	if err != nil {
		t.Fatalf("error while clearing cache: %s", err)
	}

}

// TestDiskStorageCleanupPathEscape is a deterministic regression test: Cleanup
// and ExpiredEntryCount must not touch files outside BasePath. They used to join
// the bare key onto BasePath with no validation. A Config key such as
// "../sibling" then made CacheFunk.Cleanup delete arbitrary files on disk.
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

	// both keys are rejected by the key rule: ".." may not appear anywhere, so a
	// key cannot climb out of the cache root
	for _, key := range []string{"../sibling", "sub/../../sibling"} {
		if err := storage.Cleanup(context.Background(), key, DefaultKeyConfig, expireBeforeTime); err == nil {
			t.Errorf("expected Cleanup with key %q to be rejected, but got nil error", key)
		}
		if _, err := storage.ExpiredEntryCount(context.Background(), key, DefaultKeyConfig, expireBeforeTime); err == nil {
			t.Errorf("expected ExpiredEntryCount with key %q to be rejected, but got nil error", key)
		}
		if err := storage.Set(context.Background(), key, DefaultKeyConfig, "", []byte("value"), expireBeforeTime); err == nil {
			t.Errorf("expected Set with key %q to be rejected, but got nil error", key)
		}
		if _, err := os.Stat(victim); err != nil {
			t.Errorf("file outside the cache root was affected by key %q: %s", key, err)
		}
	}
}

// TestDiskStorageErrorChain checks that errors from the operating system stay in
// the returned error chain, so callers can use errors.As and errors.Is on them.
func TestDiskStorageErrorChain(t *testing.T) {
	basePath := t.TempDir()
	storage, err := NewDiskStorage(basePath, nil)
	if err != nil {
		t.Fatalf("error while creating disk storage: %s", err)
	}

	// put a file where the entry path needs a directory
	dirs := SHA256HexPather("params")
	blocker := filepath.Join(basePath, "key", dirs[0])
	if err := os.MkdirAll(filepath.Dir(blocker), 0755); err != nil {
		t.Fatalf("error while creating blocker directory: %s", err)
	}
	if err := os.WriteFile(blocker, []byte("in the way"), 0644); err != nil {
		t.Fatalf("error while creating blocker file: %s", err)
	}

	err = storage.Set(context.Background(), "key", DefaultKeyConfig, "params", []byte("value"), time.Now().UTC())
	if err == nil {
		t.Fatal("expected Set to fail when a file is in the way of the entry directory")
	}

	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) {
		t.Errorf("expected the os error to stay in the chain but got %v", err)
	}
}

// TestDiskStorageConcurrentSetGetNoTornReads is a probabilistic regression test
// for atomic Set: a concurrent Get must never observe a partially written entry.
// Before Set wrote to a temp file and renamed it, os.WriteFile truncated the
// target first. Readers could then return a short or empty value with a nil
// error. A torn read is not guaranteed on every run. Rewriting a 1 MiB value a
// few hundred times while four goroutines read makes it very likely.
func TestDiskStorageConcurrentSetGetNoTornReads(t *testing.T) {
	config := &KeyConfig{BodyCompression: NoCompression, BodyCodec: StringCodec}

	storage, err := NewDiskStorage(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("error while creating disk storage: %s", err)
	}

	const key = "concurrent"
	want := bytes.Repeat([]byte("0123456789abcdef"), 1<<16) // 1 MiB

	// Prime the entry so readers never race against the very first write.
	if err := storage.Set(context.Background(), key, config, "", want, time.Now().UTC()); err != nil {
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
				got, err := storage.Get(context.Background(), key, config, "", notExpired)
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
		if err := storage.Set(context.Background(), key, config, "", want, time.Now().UTC()); err != nil {
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
