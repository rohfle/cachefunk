package cachefunk_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/rohfle/cachefunk"
)

func runTestCachePoisoning(t *testing.T, cache cachefunk.Cache) {

	type BadParams struct {
		Bad func()
	}

	badFunction := func(ctx *context.Context, ignoreCache bool, params *BadParams) (func(), error) {
		return func() {}, nil
	}

	goodFunction := func(ctx *context.Context, ignoreCache bool, params *BadParams) (string, error) {
		return "", nil
	}

	BadFunction := cachefunk.Wrap(badFunction, cache, cachefunk.Config{
		Key: "bad",
		TTL: 1,
	})

	GoodFunction := cachefunk.WrapString(goodFunction, cache, cachefunk.Config{
		Key: "good",
		TTL: 1,
	})

	ctx := context.TODO()

	_, err := BadFunction(&ctx, false, &BadParams{Bad: func() {}})
	if err == nil {
		t.Fatal("expected error for unserializable params")
	}

	_, err = GoodFunction(&ctx, false, &BadParams{Bad: func() {}})
	if err == nil {
		t.Fatal("expected error for unserializable params")
	}

	_, err = BadFunction(&ctx, false, nil)
	if err == nil {
		t.Fatal("expected error for function that returns unserializable result")
	}

}

func runTestCacheFuncTTL(t *testing.T, cache cachefunk.Cache, expireAllEntries func(bool)) {

	noop := func(ctx *context.Context, ignoreCache bool, params *HelloWorldParams) (string, error) {
		return "", nil
	}

	CacheTTL := cachefunk.WrapString(noop, cache, cachefunk.Config{
		Key:       "noop1",
		TTL:       1,
		TTLJitter: 0,
	})

	NoCache := cachefunk.WrapString(noop, cache, cachefunk.Config{
		Key:       "noop2",
		TTL:       0,
		TTLJitter: 0,
	})

	CacheForever := cachefunk.WrapString(noop, cache, cachefunk.Config{
		Key:       "noop3",
		TTL:       -1,
		TTLJitter: 0,
	})

	CacheTTLWithJitter := cachefunk.WrapString(noop, cache, cachefunk.Config{
		Key:       "noop4",
		TTL:       1,
		TTLJitter: 1,
	})

	ctx := context.TODO()

	NoCache(&ctx, false, nil)
	if cache.EntryCount() != 0 {
		t.Fatal("expected 0 cache entries after NoCache() but got", cache.EntryCount())
	}

	// Test TTL=-1 (cache forever)
	CacheForever(&ctx, false, nil)
	if cache.EntryCount() != 1 {
		t.Fatal("expected 1 cache entries after CacheForever() but got", cache.EntryCount())
	}

	// Test TTL=1 no jitter
	CacheTTL(&ctx, false, nil)
	if cache.EntryCount() != 2 {
		t.Fatal("expected 2 cache entries after CacheTTL() but got", cache.EntryCount())
	}

	// Wait for entries to expire
	// Check entries expiry are after now
	if count := cache.ExpiredEntryCount(nil); count != 0 {
		t.Fatal("expected 0 expired cache entries but found", count)
	}

	// Expire entries so we don't have to wait
	expireAllEntries(false)
	// Call with TTL=1 again, should delete old cache entry as expired and save new cache entry
	CacheTTL(&ctx, false, nil)
	if count := cache.ExpiredEntryCount(nil); count != 0 {
		t.Fatal("expected 0 expired cache entries but found", count)
	}
	// Expire entries so we don't have to wait
	expireAllEntries(false)
	if count := cache.ExpiredEntryCount(nil); count != 1 {
		t.Fatal("expected 1 expired cache entries but found", count)
	}
	cache.Cleanup(nil)
	if cache.EntryCount() != 1 {
		t.Fatal("expected 1 cache entries after cache cleanup but got", cache.EntryCount())
	}

	// Test jitter
	CacheTTLWithJitter(&ctx, false, nil)
	cutoff := time.Now().UTC().Add(1 * time.Second)
	if count := cache.ExpiredEntryCount(&cutoff); count != 0 {
		t.Fatal("after CacheTTLWithJitter expected 0 expired cache entries but found", count)
	}
}

func runTestCacheFuncErrorsReturned(t *testing.T, cache cachefunk.Cache) {

	failWorld := func(ctx *context.Context, ignoreCache bool, params *HelloWorldParams) (string, error) {
		return "", errors.New("oh no")
	}

	FailWorldString := cachefunk.WrapString(failWorld, cache, cachefunk.Config{
		Key:       "failWorld",
		TTL:       5,
		TTLJitter: 1,
	})

	ctx := context.TODO()

	if _, err := FailWorldString(&ctx, false, nil); err == nil {
		t.Fatal("expected an error but got nil")
	}

	if cache.EntryCount() != 0 {
		t.Fatal("expected 0 cache entries but got", cache.EntryCount())
	}

	FailWorldJSON := cachefunk.Wrap(failWorld, cache, cachefunk.Config{
		Key:       "failWorld",
		TTL:       5,
		TTLJitter: 1,
	})

	if _, err := FailWorldJSON(&ctx, false, nil); err == nil {
		t.Fatal("expected an error but got nil")
	}

	if cache.EntryCount() != 0 {
		t.Fatal("expected 0 cache entries but got", cache.EntryCount())
	}
}

func runTestWrapString(t *testing.T, cache cachefunk.Cache) {
	helloCounter := 0
	helloWorld := func(ctx *context.Context, ignoreCache bool, params *HelloWorldParams) (string, error) {
		helloCounter += 1
		s := fmt.Sprintf("Hello %s, you are %d", params.Name, params.Age)
		return s, nil
	}

	HelloWorld := cachefunk.WrapString(helloWorld, cache, cachefunk.Config{
		Key:       "helloWorld",
		TTL:       5,
		TTLJitter: 1,
	})

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

	ctx := context.TODO()

	for line, tc := range testCases {
		result, err := HelloWorld(&ctx, tc.ignoreCache, &HelloWorldParams{
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

func runTestWrap(t *testing.T, cache cachefunk.Cache) {
	helloCounter := 0
	type HelloWorldResult struct {
		Result string
		Params *HelloWorldParams
	}

	helloWorld := func(ctx *context.Context, ignoreCache bool, params *HelloWorldParams) (*HelloWorldResult, error) {
		helloCounter += 1
		s := fmt.Sprintf("Hello %s, you are %d", params.Name, params.Age)
		return &HelloWorldResult{
			Params: params,
			Result: s,
		}, nil
	}

	HelloWorld := cachefunk.Wrap(helloWorld, cache, cachefunk.Config{
		Key:       "helloWorld",
		TTL:       5,
		TTLJitter: 1,
	})

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

	ctx := context.TODO()

	for line, tc := range testCases {
		result, err := HelloWorld(&ctx, tc.ignoreCache, tc.params)

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

	if cacheEntries := cache.EntryCount(); cacheEntries != 3 {
		t.Fatalf("expected %d cached values got %d", 3, cacheEntries)
	}

	HelloWorld2 := cachefunk.Wrap(helloWorld, cache, cachefunk.Config{
		Key: "helloWorld2",
		TTL: 5,
	})

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
		result, err := HelloWorld2(&ctx, tc.ignoreCache, tc.params)

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

	if cacheEntries := cache.EntryCount(); cacheEntries != 0 {
		t.Fatalf("expected %d cached values after clear got %d", 0, cacheEntries)
	}
}
