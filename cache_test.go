package cachefunk

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"
)

// dumpStorage prints a sample of what a storage holds, for the tests that show
// the cache when they fail. Dump is not part of CacheStorage, so the storages
// that provide it are asserted below.
func dumpStorage(storage CacheStorage, n int64) {
	if dumper, ok := storage.(interface{ dump(int64) }); ok {
		dumper.dump(n)
	}
}

// the in-package storages offer the debug dump the tests use, checked at compile
// time so the helper above cannot silently print nothing
var (
	_ interface{ dump(int64) } = (*InMemoryStorage)(nil)
	_ interface{ dump(int64) } = (*DiskStorage)(nil)
	_ interface{ dump(int64) } = (*GORMStorage)(nil)
)

// runTestCleanupUnconfiguredKey checks that expired counting and cleanup are
// driven by the keys that have entries in the storage rather than the keys
// listed in Config. An unlisted key resolves through DefaultKey, and its
// entries must still be counted and cleaned up.
func runTestCleanupUnconfiguredKey(t *testing.T, cache *CacheFunk, expireAllEntries func()) {
	cache.Config = Config{
		DefaultKey: {TTL: 1, TTLJitter: 0, BodyCodec: StringCodec, BodyCompression: NoCompression},
	}

	unlisted := func(ignoreCache bool, params *HelloWorldParams) (string, error) {
		return "unlisted value", nil
	}
	Unlisted := WrapWithIgnore(cache, "unlistedKey", unlisted)

	value, err := Unlisted(false, nil)
	if err != nil {
		t.Fatalf("unexpected error while caching under an unlisted key: %s", err)
	}
	if value != "unlisted value" {
		t.Fatalf("expected \"unlisted value\" but got %q", value)
	}

	keys, err := cache.Storage.Keys()
	if err != nil {
		t.Fatalf("error while fetching cache keys: %s", err)
	}
	if len(keys) != 1 || keys[0] != "unlistedKey" {
		t.Fatalf("expected Keys() to return [unlistedKey] but got %v", keys)
	}

	// expiry is applied out of band so the test does not have to wait for it
	expireAllEntries()

	expired, err := cache.ExpiredEntryCount()
	if err != nil {
		t.Fatalf("error while fetching expired entry count: %s", err)
	}
	if expired != 1 {
		t.Errorf("expected the unlisted key to be counted as expired but got %d", expired)
	}

	cache.Cleanup(true)

	count, err := cache.EntryCount()
	if err != nil {
		t.Fatalf("expected 0 entries after cleanup but got error %s", err)
	}
	if count != 0 {
		t.Errorf("expected cleanup to remove the unlisted key's entry but %d entries remain", count)
	}

	keys, err = cache.Storage.Keys()
	if err != nil {
		t.Fatalf("error while fetching cache keys: %s", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected no keys after cleanup but got %v", keys)
	}
}

// failingSetStorage is a cache that reads as empty and cannot be written to,
// which is what a full disk or an unavailable database looks like
type failingSetStorage struct {
	setErr error
}

func (s *failingSetStorage) Get(key string, config *KeyConfig, params string, expireBeforeTime time.Time) ([]byte, error) {
	return nil, ErrEntryNotFound
}
func (s *failingSetStorage) Set(key string, config *KeyConfig, params string, value []byte, timestamp time.Time) error {
	return s.setErr
}
func (s *failingSetStorage) Keys() ([]string, error) { return nil, nil }
func (s *failingSetStorage) EntryCount() (int64, error) {
	return 0, nil
}
func (s *failingSetStorage) ExpiredEntryCount(key string, config *KeyConfig, expireBeforeTime time.Time) (int64, error) {
	return 0, nil
}
func (s *failingSetStorage) Clear() error { return nil }
func (s *failingSetStorage) Cleanup(key string, config *KeyConfig, expireBeforeTime time.Time) error {
	return nil
}

// TestCacheWriteFailureReturnsResult checks that a storage which cannot be written
// to does not fail the call: the resolved value is returned uncached, and the next
// call resolves again rather than reading a half written entry.
func TestCacheWriteFailureReturnsResult(t *testing.T) {
	cache := &CacheFunk{
		Config:  Config{},
		Storage: &failingSetStorage{setErr: errors.New("no space left on device")},
	}

	calls := 0
	Fetch := Wrap(cache, "writeFailure", func(params string) (string, error) {
		calls++
		return "resolved value", nil
	})

	if value, err := Fetch("p"); err != nil || value != "resolved value" {
		t.Fatalf("expected the resolved value despite the failed write but got %q, %v", value, err)
	}
	if value, err := Fetch("p"); err != nil || value != "resolved value" || calls != 2 {
		t.Errorf("expected nothing to be cached, so a second resolve, but got %q after %d calls (%v)", value, calls, err)
	}
}

// TestCacheErrDoNotCache checks that a resolver can return a result that must
// not be stored while the caller still receives that result, whether the
// resolver returns ErrDoNotCache directly or wraps it with %w.
func runTestCacheErrDoNotCache(t *testing.T, cache *CacheFunk) {
	cache.Config = Config{
		DefaultKey: {TTL: 60, TTLJitter: 0, BodyCodec: StringCodec, BodyCompression: NoCompression},
	}

	for _, tc := range []struct {
		name string
		err  func() error
	}{
		{"added", func() error { return ErrDoNotCache }},
		{"wrapped", func() error { return fmt.Errorf("gateway timeout: %w", ErrDoNotCache) }},
	} {
		// a fresh resolver per case: whether the result was stored is read off
		// the number of calls the resolver receives
		calls := 0
		Fetch := Wrap(cache, "errDoNotCache", func(params string) (string, error) {
			calls++
			return "uncached " + params, tc.err()
		})

		for call := 1; call <= 2; call++ {
			value, err := Fetch("p")
			if err != nil {
				t.Fatalf("%s: call %d returned an error: %s", tc.name, call, err)
			}
			if value != "uncached p" {
				t.Fatalf("%s: call %d expected \"uncached p\" but got %q", tc.name, call, value)
			}
		}

		if calls != 2 {
			t.Errorf("%s: expected the resolver to run for both calls but it ran %d times", tc.name, calls)
		}
	}

	count, err := cache.EntryCount()
	if err != nil {
		t.Fatalf("error while fetching entry count: %s", err)
	}
	if count != 0 {
		t.Errorf("expected ErrDoNotCache results to not be stored but the cache holds %d entries", count)
	}
}

// runTestCacheCompressedPayload covers caching a payload that is already
// compressed on the wire (see the README section "Caching already-compressed
// payloads"): StringCodec with NoCompression stores the bytes exactly as
// received, and the content encoding is part of the params so that entries for
// the different encodings cannot collide.
func runTestCacheCompressedPayload(t *testing.T, cache *CacheFunk) {
	cache.Config = Config{
		"upstream": {TTL: 3600, BodyCodec: StringCodec, BodyCompression: NoCompression},
	}

	type Params struct {
		URL      string
		Encoding string
	}

	// the upstream body as it arrives on the wire
	var wire bytes.Buffer
	writer := gzip.NewWriter(&wire)
	if _, err := writer.Write([]byte(`{"hello":"world","n":12345}`)); err != nil {
		t.Fatalf("unexpected error while building the test payload: %s", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("unexpected error while building the test payload: %s", err)
	}
	body := wire.Bytes()

	calls := 0
	fetch := func(ignoreCache bool, params *Params) ([]byte, error) {
		calls++
		return body, nil
	}
	Fetch := WrapWithIgnore(cache, "upstream", fetch)

	first, err := Fetch(false, &Params{URL: "/api/thing", Encoding: "gzip"})
	if err != nil {
		t.Fatalf("unexpected error while caching a compressed payload: %s", err)
	}
	if !bytes.Equal(first, body) {
		t.Errorf("expected the payload to come back unchanged but got %d bytes instead of %d", len(first), len(body))
	}

	second, err := Fetch(false, &Params{URL: "/api/thing", Encoding: "gzip"})
	if err != nil {
		t.Fatalf("unexpected error on the second call: %s", err)
	}
	if calls != 1 {
		t.Errorf("expected the second call to be served from the cache but the resolver was called %d times", calls)
	}
	if !bytes.Equal(second, body) {
		t.Errorf("cached payload is not byte identical to what was stored")
	}

	// the payload is still compressed: a consumer can decode it even though the
	// cache never decompressed it
	reader, err := gzip.NewReader(bytes.NewReader(second))
	if err != nil {
		t.Fatalf("cached payload is no longer a valid gzip stream: %s", err)
	}
	decoded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("unexpected error while decoding the cached payload: %s", err)
	}
	if string(decoded) != `{"hello":"world","n":12345}` {
		t.Errorf("decoded cached payload is wrong: %s", decoded)
	}

	// a different content encoding is a different cache entry
	if _, err := Fetch(false, &Params{URL: "/api/thing", Encoding: "identity"}); err != nil {
		t.Fatalf("unexpected error while caching a second encoding: %s", err)
	}
	count, err := cache.EntryCount()
	if err != nil {
		t.Fatalf("expected 2 entries but got error %s", err)
	}
	if count != 2 {
		t.Errorf("expected the content encoding to be part of the cache key but got %d entries", count)
	}
}

func runTestCachePoisoning(t *testing.T, cache *CacheFunk) {
	cache.Config = Config{
		"bad":  {TTL: 1},
		"good": {TTL: 1},
	}

	type BadParams struct {
		Bad func()
	}

	badFunction := func(ignoreCache bool, params *BadParams) (func(), error) {
		return func() {}, nil
	}

	goodFunction := func(ignoreCache bool, params *BadParams) (string, error) {
		return "", nil
	}

	BadFunction := WrapWithIgnore(cache, "bad", badFunction)
	GoodFunction := WrapWithIgnore(cache, "good", goodFunction)

	_, err := BadFunction(false, &BadParams{Bad: func() {}})
	if err == nil {
		t.Fatal("expected error for unserializable params")
	}

	_, err = GoodFunction(false, &BadParams{Bad: func() {}})
	if err == nil {
		t.Fatal("expected error for unserializable params")
	}

	_, err = BadFunction(false, nil)
	if err == nil {
		t.Fatal("expected error for function that returns unserializable result")
	}

	badFunctionCtx := func(ctx context.Context, params *BadParams) (func(), error) {
		return func() {}, nil
	}

	goodFunctionCtx := func(ctx context.Context, params *BadParams) (string, error) {
		return "", nil
	}

	BadFunctionCtx := WrapWithContext(cache, "bad", badFunctionCtx)
	GoodFunctionCtx := WrapWithContext(cache, "good", goodFunctionCtx)
	ctx := SetIgnoreCacheInContext(context.TODO(), true)

	_, err = BadFunctionCtx(ctx, &BadParams{Bad: func() {}})
	if err == nil {
		t.Fatal("expected error for unserializable params")
	}

	_, err = GoodFunctionCtx(ctx, &BadParams{Bad: func() {}})
	if err == nil {
		t.Fatal("expected error for unserializable params")
	}

	_, err = BadFunctionCtx(ctx, nil)
	if err == nil {
		t.Fatal("expected error for function that returns unserializable result")
	}

}

func runTestCacheFuncTTL(t *testing.T, cache *CacheFunk, expireAllEntries func()) {
	cache.Config = Config{
		"noop_NoCache": {
			TTL:             TTLEntryImmediatelyExpires,
			TTLJitter:       0,
			BodyCodec:       StringCodec,
			BodyCompression: NoCompression,
		},
		"noop_CacheTTL": {
			TTL:             1,
			TTLJitter:       0,
			BodyCodec:       StringCodec,
			BodyCompression: NoCompression,
		},
		"noop_CacheTTLWithJitter": {
			TTL:             30,
			TTLJitter:       1,
			BodyCodec:       StringCodec,
			BodyCompression: NoCompression,
		},
		"noop_CacheTTLNegative": {
			TTL:             -2121,
			TTLJitter:       1,
			BodyCodec:       StringCodec,
			BodyCompression: NoCompression,
		},
		"noop_CacheTTLHuge": {
			TTL:             9223370000,
			TTLJitter:       1,
			BodyCodec:       StringCodec,
			BodyCompression: NoCompression,
		},
		"noop_CacheTTLForever": {
			TTL:             TTLEntryNeverExpires,
			TTLJitter:       1,
			BodyCodec:       StringCodec,
			BodyCompression: NoCompression,
		},
	}

	callCount := 0
	noop := func(ignoreCache bool, params *HelloWorldParams) ([]byte, error) {
		callCount += 1
		return []byte{}, nil
	}

	CacheTTL := WrapWithIgnore(cache, "noop_CacheTTL", noop)
	NoCache := WrapWithIgnore(cache, "noop_NoCache", noop)
	CacheTTLWithJitter := WrapWithIgnore(cache, "noop_CacheTTLWithJitter", noop)
	CacheTTLNegative := WrapWithIgnore(cache, "noop_CacheTTLNegative", noop)
	CacheTTLHuge := WrapWithIgnore(cache, "noop_CacheTTLHuge", noop)
	CacheTTLForever := WrapWithIgnore(cache, "noop_CacheTTLForever", noop)

	testCases := []struct {
		key                     string
		fn                      func(bool, *HelloWorldParams) ([]byte, error)
		firstCallCount          int
		firstEntryCount         int64
		firstEntryExpiredCount  int64
		secondCallCount         int
		secondEntryCount        int64
		secondEntryExpiredCount int64
	}{
		{"noop_NoCache", NoCache, 1, 0, 0, 1, 0, 0},
		{"noop_CacheTTL", CacheTTL, 1, 1, 0, 1, 1, 0},
		{"noop_CacheTTLWithJitter", CacheTTLWithJitter, 1, 1, 0, 1, 1, 0},
		{"noop_CacheTTLNegative", CacheTTLNegative, 1, 1, 0, 0, 1, 0},
		{"noop_CacheTTLHuge", CacheTTLHuge, 1, 1, 0, 0, 1, 0},
		{"noop_CacheTTLForever", CacheTTLForever, 1, 1, 0, 0, 1, 0},
	}
	for line, tc := range testCases {

		cache.Clear()
		callCount = 0
		tc.fn(false, nil)
		if callCount != tc.firstCallCount {
			t.Errorf("line %d: expected %d callCount after first call to %s but got %d", line+1, tc.firstCallCount, tc.key, callCount)
		}

		count, err := cache.EntryCount()
		if err != nil {
			t.Fatalf("line %d: expected %d entries after first call to %s but got error %s", line+1, tc.firstEntryCount, tc.key, err)
		}
		if count != tc.firstEntryCount {
			t.Errorf("line %d: expected %d entries after first call to %s but got %d", line+1, tc.firstEntryCount, tc.key, count)
		}

		count, err = cache.ExpiredEntryCount()
		if err != nil {
			t.Fatalf("line %d: expected %d expired entries after first call to %s but got error %s", line+1, tc.firstEntryExpiredCount, tc.key, err)
		}
		if count != tc.firstEntryExpiredCount {
			t.Errorf("line %d: expected %d expired entries after first call to %s but got %d", line+1, tc.firstEntryExpiredCount, tc.key, count)
		}

		if t.Failed() {
			return
		}

		expireAllEntries()

		callCount = 0
		tc.fn(false, nil)
		if callCount != tc.secondCallCount {
			t.Errorf("line %d: expected %d callCount after second call to %s but got %d", line+1, tc.secondCallCount, tc.key, callCount)
		}

		count, err = cache.EntryCount()
		if err != nil {
			t.Fatalf("line %d: expected %d entries after second call to %s but got error %s", line+1, tc.secondEntryCount, tc.key, err)
		}
		if count != tc.secondEntryCount {
			t.Errorf("line %d: expected %d entries after second call to %s but got %d", line+1, tc.secondEntryCount, tc.key, count)
		}

		count, err = cache.ExpiredEntryCount()
		if err != nil {
			t.Fatalf("line %d: expected %d expired entries after second call to %s but got error %s", line+1, tc.secondEntryExpiredCount, tc.key, err)
		}
		if count != tc.secondEntryExpiredCount {
			t.Errorf("line %d: expected %d expired entries after second call to %s but got %d", line+1, tc.secondEntryExpiredCount, tc.key, count)
		}

		cache.Cleanup(true)

		if t.Failed() {
			return
		}
	}

}

func runTestCacheMismatchCompressionType(t *testing.T, cache *CacheFunk, expireAllEntries func()) {
	cache.Config = Config{
		"hello_CacheTTL": {TTL: 1, TTLJitter: 0, FallbackToExpired: true, BodyCompression: ZstdCompression},
	}

	callCount := 0
	helloRaw := func(ignoreCache bool, params *HelloWorldParams) (string, error) {
		callCount += 1
		return "hello world", nil
	}

	CacheTTL := WrapWithIgnore(cache, "hello_CacheTTL", helloRaw)

	// Test TTL=1 no jitter
	firstValue, err := CacheTTL(false, nil)
	if err != nil {
		t.Fatal("unexpected error on first call to CacheTTL", err)
	}

	count, err := cache.EntryCount()
	if err != nil {
		t.Fatal("expected 1 cache entries after CacheTTL() but got error", err)
	}
	if count != 1 {
		t.Fatal("expected 1 cache entries after CacheTTL() but got", count)
	}

	cache.Config["hello_CacheTTL"] = &KeyConfig{
		TTL:             1,
		TTLJitter:       0,
		BodyCompression: NoCompression,
	}

	secondValue, err := CacheTTL(false, nil)
	if err != nil {
		t.Fatal("unexpected error on second call to CacheTTL:", err)
	}
	if callCount != 2 {
		t.Fatal("expected 2 callCount after second call to CacheTTL but got", callCount)
	}
	if firstValue != secondValue {
		t.Fatalf("returned values from CacheTTL do not match: %q vs %q", firstValue, secondValue)
	}
	cache.Clear()
}

func runTestCacheFallBackToExpired(t *testing.T, cache *CacheFunk, expireAllEntries func()) {
	cache.Config = Config{
		"hello_CacheTTL": {TTL: 1, TTLJitter: 0, FallbackToExpired: true},
	}

	callCount := 0
	helloRaw := func(ignoreCache bool, params *HelloWorldParams) (string, error) {
		callCount += 1
		if callCount <= 1 {
			return "hello world", nil
		} else {
			return "", errors.New("fake upstream error")
		}
	}

	CacheTTL := WrapWithIgnore(cache, "hello_CacheTTL", helloRaw)

	// Test TTL=1 no jitter
	firstValue, err := CacheTTL(false, nil)
	if err != nil {
		t.Fatal("unexpected error on first call to CacheTTL", err)
	}
	count, err := cache.EntryCount()
	if err != nil {
		t.Fatal("expected 1 cache entries after CacheTTL() but got error", err)
	}
	if count != 1 {
		t.Fatal("expected 1 cache entries after CacheTTL() but got", count)
	}

	// Wait for entries to expire
	// Check entries expireBeforeTime are after now
	count, err = cache.ExpiredEntryCount()
	if err != nil {
		t.Fatal("expected 0 expired cache entries but got error", err)
	}
	if count != 0 {
		t.Fatal("expected 0 expired cache entries but found", count)
	}

	// Expire entries so we don't have to wait
	expireAllEntries()
	// Call again, this time it will fail
	DisableWarnings()
	secondValue, err := CacheTTL(false, nil)
	EnableWarnings()
	if err != nil {
		t.Fatal("unexpected error on second call to CacheTTL:", err)
	}
	count, err = cache.ExpiredEntryCount()
	if err != nil {
		t.Fatal("expected 1 expired cache entries but got error", err)
	}
	if count != 1 {
		t.Fatal("expected 1 expired cache entries but found", count)
	}
	if firstValue != secondValue {
		t.Fatalf("returned values from CacheTTL do not match: %q vs %q", firstValue, secondValue)
	}

	// When FallbackToExpired is set, Cleanup does not remove expired entries when cleanupExpired is false
	cache.Cleanup(false)
	count, err = cache.EntryCount()
	if err != nil {
		t.Fatal("expected 1 cache entries after cache.Cleanup(false) but got error", err)
	}
	if count != 1 {
		t.Fatal("expected 1 cache entries after cache.Cleanup(false) but got", count)
	}

	// Force cleanup of expired entries
	cache.Cleanup(true)
	count, err = cache.EntryCount()
	if err != nil {
		t.Fatal("expected 0 cache entries after cache.Cleanup(true) but got error", err)
	}
	if count != 0 {
		t.Fatal("expected 0 cache entries after cache.Cleanup(true) but got", count)
	}
}

func runTestCacheFallBackToExpiredWithContext(t *testing.T, cache *CacheFunk, expireAllEntries func()) {
	cache.Config = Config{
		"hello_CacheTTL": {TTL: 1, TTLJitter: 0, FallbackToExpired: true},
	}

	callCount := 0
	helloRaw := func(ctx context.Context, params *HelloWorldParams) (string, error) {
		callCount += 1
		if callCount <= 1 {
			return "hello world", nil
		} else {
			return "", errors.New("fake upstream error")
		}
	}

	CacheTTL := WrapWithContext(cache, "hello_CacheTTL", helloRaw)

	// Test TTL=1 no jitter
	firstValue, err := CacheTTL(context.TODO(), nil)
	if err != nil {
		t.Fatal("unexpected error on first call to CacheTTL", err)
	}
	count, err := cache.EntryCount()
	if err != nil {
		t.Fatal("expected 1 cache entries after CacheTTL() but got error", err)
	}
	if count != 1 {
		t.Fatal("expected 1 cache entries after CacheTTL() but got", count)
	}

	// Wait for entries to expire
	// Check entries expireBeforeTime are after now
	count, err = cache.ExpiredEntryCount()
	if err != nil {
		t.Fatal("expected 0 expired cache entries but got error", err)
	}
	if count != 0 {
		t.Fatal("expected 0 expired cache entries but found", count)
	}

	// Expire entries so we don't have to wait
	expireAllEntries()
	// Call again, this time it will fail
	DisableWarnings()
	secondValue, err := CacheTTL(context.TODO(), nil)
	EnableWarnings()
	if err != nil {
		t.Fatal("unexpected error on second call to CacheTTL:", err)
	}
	count, err = cache.ExpiredEntryCount()
	if err != nil {
		t.Fatal("expected 1 expired cache entries but got error", err)
	}
	if count != 1 {
		t.Fatal("expected 1 expired cache entries but found", count)
	}
	if firstValue != secondValue {
		t.Fatalf("returned values from CacheTTL do not match: %q vs %q", firstValue, secondValue)
	}

	cache.Cleanup(false)
	count, err = cache.EntryCount()
	if err != nil {
		t.Fatal("expected 1 cache entries after cache.Cleanup(false) but got error", err)
	}
	if count != 1 {
		t.Fatal("expected 1 cache entries after cache.Cleanup(false) but got", count)
	}

	cache.Cleanup(true)
	count, err = cache.EntryCount()
	if err != nil {
		t.Fatal("expected 0 cache entries after cache.Cleanup(true) but got error", err)
	}
	if count != 0 {
		t.Fatal("expected 0 cache entries after cache.Cleanup(true) but got", count)
	}
}

func runTestCacheFuncErrorsReturned(t *testing.T, cache *CacheFunk) {
	cache.Config = Config{
		"failWorld": {TTL: 5, TTLJitter: 1},
	}

	failWorld := func(ignoreCache bool, params *HelloWorldParams) (string, error) {
		return "", errors.New("oh no")
	}

	FailWorldString := WrapWithIgnore(cache, "failWorld", failWorld)

	if _, err := FailWorldString(false, nil); err == nil {
		t.Fatal("expected an error but got nil")
	}

	count, err := cache.EntryCount()
	if err != nil {
		t.Fatal("expected 0 cache entries but got error", err)
	}
	if count != 0 {
		t.Fatal("expected 0 cache entries but got", count)
	}

	FailWorldJSON := WrapWithIgnore(cache, "failWorld", failWorld)

	if _, err := FailWorldJSON(false, nil); err == nil {
		t.Fatal("expected an error but got nil")
	}

	count, err = cache.EntryCount()
	if err != nil {
		t.Fatal("expected 0 cache entries but got error", err)
	}
	if count != 0 {
		t.Fatal("expected 0 cache entries but got", count)
	}
}

func runTestCacheFuncWithContextErrorsReturned(t *testing.T, cache *CacheFunk) {
	cache.Config = Config{
		"failWorld": {TTL: 5, TTLJitter: 1},
	}

	failWorld := func(ctx context.Context, params *HelloWorldParams) (string, error) {
		return "", errors.New("oh no")
	}

	FailWorldString := WrapWithContext(cache, "failWorld", failWorld)

	if _, err := FailWorldString(context.TODO(), nil); err == nil {
		t.Fatal("expected an error but got nil")
	}

	count, err := cache.EntryCount()
	if err != nil {
		t.Fatal("expected 0 cache entries but got error", err)
	}
	if count != 0 {
		t.Fatal("expected 0 cache entries but got", count)
	}

	FailWorldJSON := WrapWithContext(cache, "failWorld", failWorld)

	if _, err := FailWorldJSON(context.TODO(), nil); err == nil {
		t.Fatal("expected an error but got nil")
	}

	count, err = cache.EntryCount()
	if err != nil {
		t.Fatal("expected 0 cache entries but got error", err)
	}
	if count != 0 {
		t.Fatal("expected 0 cache entries but got", count)
	}
}

func runTestWrapWithStringResult(t *testing.T, cache *CacheFunk) {
	cache.Config = Config{
		"helloWorld": {TTL: 5, TTLJitter: 1, BodyCompression: NoCompression},
	}

	helloCounter := 0
	helloWorld := func(ignoreCache bool, params *HelloWorldParams) (string, error) {
		helloCounter += 1
		s := fmt.Sprintf("Hello %s, you are %d", params.Name, params.Age)
		return s, nil
	}

	HelloWorld := WrapWithIgnore(cache, "helloWorld", helloWorld)

	testCases := []struct {
		ignoreCache bool
		name        string
		age         int64
		result      string
		err         error
		counter     int
	}{
		{false, "Bob", 42, "Hello Bob, you are 42", nil, 1},
		{false, "Clark", 24, "Hello Clark, you are 24", nil, 2},
		{false, "Bob", 43, "Hello Bob, you are 43", nil, 3},
		{false, "Bob", 42, "Hello Bob, you are 42", nil, 3},
		{true, "Bob", 42, "Hello Bob, you are 42", nil, 4},
	}

	for line, tc := range testCases {
		result, err := HelloWorld(tc.ignoreCache, &HelloWorldParams{
			Name: tc.name,
			Age:  tc.age,
		})

		if err != nil {
			t.Errorf("subtest %d: call to HelloWorld returned an error: %s", line+1, err)
		}

		if helloCounter != tc.counter {
			t.Errorf("subtest %d: helloCounter expected %d got %d", line+1, tc.counter, helloCounter)
		}

		if result != tc.result {
			t.Errorf("subtest %d: result expected \"%s\" got \"%s\"", line+1, tc.result, result)
		}

		if t.Failed() {
			return
		}
	}
}

func runTestWrapWithContextAndStringResult(t *testing.T, cache *CacheFunk) {
	cache.Config = Config{
		"helloWorld": {TTL: 5, TTLJitter: 1},
	}

	helloCounter := 0
	helloWorld := func(ctx context.Context, params *HelloWorldParams) (string, error) {
		helloCounter += 1
		s := fmt.Sprintf("Hello %s, you are %d", params.Name, params.Age)
		return s, nil
	}

	HelloWorld := WrapWithContext(cache, "helloWorld", helloWorld)

	testCases := []struct {
		ignoreCache bool
		name        string
		age         int64
		result      string
		err         error
		counter     int
	}{
		{false, "Bob", 42, "Hello Bob, you are 42", nil, 1},
		{false, "Clark", 24, "Hello Clark, you are 24", nil, 2},
		{false, "Bob", 43, "Hello Bob, you are 43", nil, 3},
		{false, "Bob", 42, "Hello Bob, you are 42", nil, 3},
		{true, "Bob", 42, "Hello Bob, you are 42", nil, 4},
	}
	for line, tc := range testCases {
		ctx := SetIgnoreCacheInContext(context.TODO(), tc.ignoreCache)

		result, err := HelloWorld(ctx, &HelloWorldParams{
			Name: tc.name,
			Age:  tc.age,
		})

		if err != nil {
			t.Errorf("subtest %d: call to HelloWorld returned an error: %s", line+1, err)
		}

		if helloCounter != tc.counter {
			t.Errorf("subtest %d: helloCounter expected %d got %d", line+1, tc.counter, helloCounter)
		}

		if result != tc.result {
			t.Errorf("subtest %d: result expected \"%s\" got \"%s\"", line+1, tc.result, result)
		}

		if t.Failed() {
			return
		}
	}
}

func runTestWrapWithIgnoreAndObjectResult(t *testing.T, cache *CacheFunk) {
	cache.Config = Config{
		"helloWorld": {
			TTL:             5,
			TTLJitter:       1,
			BodyCodec:       JSONCodec,
			BodyCompression: ZstdCompression,
			ParamCodec:      JSONBase64Params,
		},
		"helloWorld2": {
			TTL:             5,
			BodyCodec:       JSONCodec,
			BodyCompression: ZstdCompression,
			ParamCodec:      JSONParams,
		},
	}

	helloCounter := 0
	type HelloWorldResult struct {
		Result string
		Params *HelloWorldParams
	}

	dumpStorage(cache.Storage, 1)

	helloWorld := func(ignoreCache bool, params *HelloWorldParams) (*HelloWorldResult, error) {
		helloCounter += 1
		s := fmt.Sprintf("Hello %s, you are %d", params.Name, params.Age)
		return &HelloWorldResult{
			Params: params,
			Result: s,
		}, nil
	}

	HelloWorld := WrapWithIgnore(cache, "helloWorld", helloWorld)

	testCases := []struct {
		ignoreCache bool
		params      *HelloWorldParams
		result      string
		err         error
		counter     int
	}{
		{false, &HelloWorldParams{"Bob", 42}, "Hello Bob, you are 42", nil, 1},
		{false, &HelloWorldParams{"Clark", 24}, "Hello Clark, you are 24", nil, 2},
		{false, &HelloWorldParams{"Bob", 43}, "Hello Bob, you are 43", nil, 3},
		{false, &HelloWorldParams{"Bob", 42}, "Hello Bob, you are 42", nil, 3},
		{true, &HelloWorldParams{"Bob", 42}, "Hello Bob, you are 42", nil, 4},
	}

	for line, tc := range testCases {
		result, err := HelloWorld(tc.ignoreCache, tc.params)

		if err != nil {
			t.Errorf("subtest %d: call to HelloWorld returned an error: %s", line+1, err)
		}

		if helloCounter != tc.counter {
			t.Errorf("subtest %d: helloCounter expected %d got %d", line+1, tc.counter, helloCounter)
		}

		if result.Result != tc.result {
			t.Errorf("subtest %d: result expected \"%s\" got \"%s\"", line+1, tc.result, result.Result)
		}

		if t.Failed() {
			return
		}
	}

	count, err := cache.EntryCount()
	if err != nil {
		t.Fatal("expected 3 cached values got error", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 cached values got %d", count)
	}

	dumpStorage(cache.Storage, 1)

	HelloWorld2 := WrapWithIgnore(cache, "helloWorld2", helloWorld)

	testCases = []struct {
		ignoreCache bool
		params      *HelloWorldParams
		result      string
		err         error
		counter     int
	}{
		{false, &HelloWorldParams{"Bob", 42}, "Hello Bob, you are 42", nil, 5},
		{false, &HelloWorldParams{"Clark", 24}, "Hello Clark, you are 24", nil, 6},
		{false, &HelloWorldParams{"Bob", 43}, "Hello Bob, you are 43", nil, 7},
		{false, &HelloWorldParams{"Bob", 42}, "Hello Bob, you are 42", nil, 7},
		{true, &HelloWorldParams{"Bob", 42}, "Hello Bob, you are 42", nil, 8},
	}

	for line, tc := range testCases {
		result, err := HelloWorld2(tc.ignoreCache, tc.params)

		if err != nil {
			t.Errorf("subtest %d: call to HelloWorld returned an error: %s", line+1, err)
		}

		if helloCounter != tc.counter {
			t.Errorf("subtest %d: helloCounter expected %d got %d", line+1, tc.counter, helloCounter)
		}

		if result.Result != tc.result {
			t.Errorf("subtest %d: result expected \"%s\" got \"%s\"", line+1, tc.result, result.Result)
		}

		if t.Failed() {
			return
		}
	}

	// test compression with bad zstd value
	keyConfig := cache.Config.Get("helloWorld2")
	params := &HelloWorldParams{"Bob", 43}
	paramsRendered, _ := keyConfig.GetParamCodec().Marshal(params)
	doctoredResult := &HelloWorldResult{
		Result: "something else",
		Params: nil,
	}
	raw, _ := json.Marshal(doctoredResult)
	cache.Storage.Set("helloWorld2", keyConfig, paramsRendered, raw, keyConfig.GetTimestamp(time.Now().UTC()))
	DisableWarnings()
	result, err := HelloWorld2(false, params)
	EnableWarnings()
	if err != nil {
		t.Errorf("testing zstd bad decompression: %s", err)
	} else if result.Result == "something else" {
		t.Errorf("got unexpected poisoned value")
	}

	cache.Clear()

	count, err = cache.EntryCount()
	if err != nil {
		t.Fatal("expected 0 cached values after clear got error", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 cached values after clear got %d", count)
	}
}

func runTestWrapWithContextAndObjectResult(t *testing.T, cache *CacheFunk) {
	cache.Config = Config{
		DefaultKey:   &KeyConfig{TTL: 5, TTLJitter: 1},
		"helloWorld": {TTL: 5, TTLJitter: 1},
	}

	helloCounter := 0
	type HelloWorldResult struct {
		Result string
		Params *HelloWorldParams
	}

	helloWorld := func(ctx context.Context, params *HelloWorldParams) (*HelloWorldResult, error) {
		helloCounter += 1
		s := fmt.Sprintf("Hello %s, you are %d", params.Name, params.Age)
		return &HelloWorldResult{
			Params: params,
			Result: s,
		}, nil
	}

	HelloWorld := WrapWithContext(cache, "helloWorld", helloWorld)

	testCases := []struct {
		ignoreCache bool
		params      *HelloWorldParams
		result      string
		err         error
		counter     int
	}{
		{false, &HelloWorldParams{"Bob", 42}, "Hello Bob, you are 42", nil, 1},
		{false, &HelloWorldParams{"Clark", 24}, "Hello Clark, you are 24", nil, 2},
		{false, &HelloWorldParams{"Bob", 43}, "Hello Bob, you are 43", nil, 3},
		{false, &HelloWorldParams{"Bob", 42}, "Hello Bob, you are 42", nil, 3},
		{true, &HelloWorldParams{"Bob", 42}, "Hello Bob, you are 42", nil, 4},
	}

	for line, tc := range testCases {
		ctx := SetIgnoreCacheInContext(context.TODO(), tc.ignoreCache)

		result, err := HelloWorld(ctx, tc.params)

		if err != nil {
			t.Errorf("subtest %d: call to HelloWorld returned an error: %s", line+1, err)
		}

		if helloCounter != tc.counter {
			t.Errorf("subtest %d: helloCounter expected %d got %d", line+1, tc.counter, helloCounter)
		}

		if result.Result != tc.result {
			t.Errorf("subtest %d: result expected \"%s\" got \"%s\"", line+1, tc.result, result.Result)
		}

		if t.Failed() {
			return
		}
	}

	count, err := cache.EntryCount()
	if err != nil {
		t.Fatal("expected 3 cached values got error", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 cached values got %d", count)
	}

	HelloWorld2 := WrapWithContext(cache, "helloWorld2", helloWorld)

	testCases = []struct {
		ignoreCache bool
		params      *HelloWorldParams
		result      string
		err         error
		counter     int
	}{
		{false, &HelloWorldParams{"Bob", 42}, "Hello Bob, you are 42", nil, 5},
		{false, &HelloWorldParams{"Clark", 24}, "Hello Clark, you are 24", nil, 6},
		{false, &HelloWorldParams{"Bob", 43}, "Hello Bob, you are 43", nil, 7},
		{false, &HelloWorldParams{"Bob", 42}, "Hello Bob, you are 42", nil, 7},
		{true, &HelloWorldParams{"Bob", 42}, "Hello Bob, you are 42", nil, 8},
	}

	for line, tc := range testCases {
		ctx := SetIgnoreCacheInContext(context.TODO(), tc.ignoreCache)

		result, err := HelloWorld2(ctx, tc.params)

		if err != nil {
			t.Errorf("subtest %d: call to HelloWorld returned an error: %s", line+1, err)
		}

		if helloCounter != tc.counter {
			t.Errorf("subtest %d: helloCounter expected %d got %d", line+1, tc.counter, helloCounter)
		}

		if result.Result != tc.result {
			t.Errorf("subtest %d: result expected \"%s\" got \"%s\"", line+1, tc.result, result.Result)
		}

		if t.Failed() {
			return
		}
	}

	cache.Clear()

	count, err = cache.EntryCount()
	if err != nil {
		t.Fatal("expected 0 cached values after clear got error", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 cached values after clear got %d", count)
	}
}

func runTestWrapAndObjectResult(t *testing.T, cache *CacheFunk) {
	cache.Config = Config{
		DefaultKey:   &KeyConfig{TTL: 5, TTLJitter: 1},
		"helloWorld": {TTL: 5, TTLJitter: 1},
	}

	helloCounter := 0
	type HelloWorldResult struct {
		Result string
		Params *HelloWorldParams
	}

	helloWorld := func(params *HelloWorldParams) (*HelloWorldResult, error) {
		helloCounter += 1
		s := fmt.Sprintf("Hello %s, you are %d", params.Name, params.Age)
		return &HelloWorldResult{
			Params: params,
			Result: s,
		}, nil
	}

	HelloWorld := Wrap(cache, "helloWorld", helloWorld)

	testCases := []struct {
		ignoreCache bool
		params      *HelloWorldParams
		result      string
		err         error
		counter     int
	}{
		{false, &HelloWorldParams{"Bob", 42}, "Hello Bob, you are 42", nil, 1},
		{false, &HelloWorldParams{"Clark", 24}, "Hello Clark, you are 24", nil, 2},
		{false, &HelloWorldParams{"Bob", 43}, "Hello Bob, you are 43", nil, 3},
		{false, &HelloWorldParams{"Bob", 42}, "Hello Bob, you are 42", nil, 3},
	}

	for line, tc := range testCases {
		result, err := HelloWorld(tc.params)

		if err != nil {
			t.Errorf("subtest %d: call to HelloWorld returned an error: %s", line+1, err)
		}

		if helloCounter != tc.counter {
			t.Errorf("subtest %d: helloCounter expected %d got %d", line+1, tc.counter, helloCounter)
		}

		if result.Result != tc.result {
			t.Errorf("subtest %d: result expected \"%s\" got \"%s\"", line+1, tc.result, result.Result)
		}

		if t.Failed() {
			return
		}
	}

	count, err := cache.EntryCount()
	if err != nil {
		t.Fatal("expected 3 cached values got error", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 cached values got %d", count)
	}

	HelloWorld2 := Wrap(cache, "helloWorld2", helloWorld)

	testCases = []struct {
		ignoreCache bool
		params      *HelloWorldParams
		result      string
		err         error
		counter     int
	}{
		{false, &HelloWorldParams{"Bob", 42}, "Hello Bob, you are 42", nil, 4},
		{false, &HelloWorldParams{"Clark", 24}, "Hello Clark, you are 24", nil, 5},
		{false, &HelloWorldParams{"Bob", 43}, "Hello Bob, you are 43", nil, 6},
		{false, &HelloWorldParams{"Bob", 42}, "Hello Bob, you are 42", nil, 6},
	}

	for line, tc := range testCases {
		result, err := HelloWorld2(tc.params)

		if err != nil {
			t.Errorf("subtest %d: call to HelloWorld returned an error: %s", line+1, err)
		}

		if helloCounter != tc.counter {
			t.Errorf("subtest %d: helloCounter expected %d got %d", line+1, tc.counter, helloCounter)
		}

		if result.Result != tc.result {
			t.Errorf("subtest %d: result expected \"%s\" got \"%s\"", line+1, tc.result, result.Result)
		}

		if t.Failed() {
			return
		}
	}

	cache.Clear()

	count, err = cache.EntryCount()
	if err != nil {
		t.Fatal("expected 0 cached values after clear got error", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 cached values after clear got %d", count)
	}
}
