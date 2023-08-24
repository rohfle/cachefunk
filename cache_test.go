package cachefunk_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"testing"
	"time"

	"github.com/rohfle/cachefunk"
)

func runTestCachePoisoning(t *testing.T, cache cachefunk.Cache) {
	cache.SetConfig(&cachefunk.CacheFunkConfig{
		Configs: map[string]*cachefunk.KeyConfig{
			"bad":  {TTL: 1},
			"good": {TTL: 1},
		},
	})

	type BadParams struct {
		Bad func()
	}

	badFunction := func(ignoreCache bool, params *BadParams) (func(), error) {
		return func() {}, nil
	}

	goodFunction := func(ignoreCache bool, params *BadParams) (string, error) {
		return "", nil
	}

	BadFunction := cachefunk.WrapObject(cache, "bad", badFunction)
	GoodFunction := cachefunk.WrapString(cache, "good", goodFunction)

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

	BadFunctionCtx := cachefunk.WrapObjectWithContext(cache, "bad", badFunctionCtx)
	GoodFunctionCtx := cachefunk.WrapStringWithContext(cache, "good", goodFunctionCtx)
	ctx := context.WithValue(context.TODO(), cachefunk.DEFAULT_IGNORE_CACHE_CTX_KEY, true)

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

func runTestCacheFuncTTL(t *testing.T, cache cachefunk.Cache, expireAllEntries func()) {
	cache.SetConfig(&cachefunk.CacheFunkConfig{
		Configs: map[string]*cachefunk.KeyConfig{
			"noop1": {TTL: 1, TTLJitter: 0},
			"noop2": {TTL: 0, TTLJitter: 0},
			"noop4": {TTL: 1, TTLJitter: 1},
		},
	})

	noop := func(ignoreCache bool, params *HelloWorldParams) (string, error) {
		return "", nil
	}

	CacheTTL := cachefunk.WrapString(cache, "noop1", noop)
	NoCache := cachefunk.WrapString(cache, "noop2", noop)
	CacheTTLWithJitter := cachefunk.WrapString(cache, "noop4", noop)

	NoCache(false, nil)
	if cache.EntryCount() != 0 {
		t.Fatal("expected 0 cache entries after NoCache() but got", cache.EntryCount())
	}

	// Test TTL=1 no jitter
	CacheTTL(false, nil)
	if cache.EntryCount() != 1 {
		t.Fatal("expected 1 cache entries after CacheTTL() but got", cache.EntryCount())
	}

	// Wait for entries to expire
	// Check entries expiry are after now
	if count := cache.ExpiredEntryCount(); count != 0 {
		t.Fatal("expected 0 expired cache entries but found", count)
	}

	// Expire entries so we don't have to wait
	expireAllEntries()
	// Call with TTL=1 again, should delete old cache entry as expired and save new cache entry
	CacheTTL(false, nil)
	if count := cache.ExpiredEntryCount(); count != 0 {
		t.Fatal("expected 0 expired cache entries but found", count)
	}
	// Expire entries so we don't have to wait
	expireAllEntries()
	if count := cache.ExpiredEntryCount(); count != 1 {
		t.Fatal("expected 1 expired cache entries but found", count)
		if thing, ok := cache.(*cachefunk.DiskCache); ok {
			thing.IterateFiles(thing.BasePath, func(parent string, file fs.DirEntry) {
				fmt.Println("WHEEE", parent, file.Name())
			})
		}
	}
	cache.Cleanup()
	if cache.EntryCount() != 0 {
		t.Fatal("expected 0 cache entries after cache cleanup but got", cache.EntryCount())
	}

	// Test jitter
	CacheTTLWithJitter(false, nil)
	if count := cache.ExpiredEntryCount(); count != 1 {
		t.Fatal("after CacheTTLWithJitter expected 1 expired cache entry but found", count)
	}
}

func runTestCacheFuncErrorsReturned(t *testing.T, cache cachefunk.Cache) {
	cache.SetConfig(&cachefunk.CacheFunkConfig{
		Configs: map[string]*cachefunk.KeyConfig{
			"failWorld": {TTL: 5, TTLJitter: 1},
		},
	})

	failWorld := func(ignoreCache bool, params *HelloWorldParams) (string, error) {
		return "", errors.New("oh no")
	}

	FailWorldString := cachefunk.WrapString(cache, "failWorld", failWorld)

	if _, err := FailWorldString(false, nil); err == nil {
		t.Fatal("expected an error but got nil")
	}

	if cache.EntryCount() != 0 {
		t.Fatal("expected 0 cache entries but got", cache.EntryCount())
	}

	FailWorldJSON := cachefunk.WrapObject(cache, "failWorld", failWorld)

	if _, err := FailWorldJSON(false, nil); err == nil {
		t.Fatal("expected an error but got nil")
	}

	if cache.EntryCount() != 0 {
		t.Fatal("expected 0 cache entries but got", cache.EntryCount())
	}
}

func runTestCacheFuncWithContextErrorsReturned(t *testing.T, cache cachefunk.Cache) {
	cache.SetConfig(&cachefunk.CacheFunkConfig{
		Configs: map[string]*cachefunk.KeyConfig{
			"failWorld": {TTL: 5, TTLJitter: 1},
		},
	})

	failWorld := func(ctx context.Context, params *HelloWorldParams) (string, error) {
		return "", errors.New("oh no")
	}

	FailWorldString := cachefunk.WrapStringWithContext(cache, "failWorld", failWorld)

	if _, err := FailWorldString(context.TODO(), nil); err == nil {
		t.Fatal("expected an error but got nil")
	}

	if cache.EntryCount() != 0 {
		t.Fatal("expected 0 cache entries but got", cache.EntryCount())
	}

	FailWorldJSON := cachefunk.WrapObjectWithContext(cache, "failWorld", failWorld)

	if _, err := FailWorldJSON(context.TODO(), nil); err == nil {
		t.Fatal("expected an error but got nil")
	}

	if cache.EntryCount() != 0 {
		t.Fatal("expected 0 cache entries but got", cache.EntryCount())
	}
}

func runTestWrapString(t *testing.T, cache cachefunk.Cache) {
	cache.SetConfig(&cachefunk.CacheFunkConfig{
		Configs: map[string]*cachefunk.KeyConfig{
			"helloWorld": {TTL: 5, TTLJitter: 1},
		},
	})

	helloCounter := 0
	helloWorld := func(ignoreCache bool, params *HelloWorldParams) (string, error) {
		helloCounter += 1
		s := fmt.Sprintf("Hello %s, you are %d", params.Name, params.Age)
		return s, nil
	}

	HelloWorld := cachefunk.WrapString(cache, "helloWorld", helloWorld)

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

func runTestWrapStringWithContext(t *testing.T, cache cachefunk.Cache) {
	cache.SetConfig(&cachefunk.CacheFunkConfig{
		Configs: map[string]*cachefunk.KeyConfig{
			"helloWorld": {TTL: 5, TTLJitter: 1},
		},
	})

	helloCounter := 0
	helloWorld := func(ctx context.Context, params *HelloWorldParams) (string, error) {
		helloCounter += 1
		s := fmt.Sprintf("Hello %s, you are %d", params.Name, params.Age)
		return s, nil
	}

	HelloWorld := cachefunk.WrapStringWithContext(cache, "helloWorld", helloWorld)

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
		ctx := context.WithValue(context.TODO(), cachefunk.DEFAULT_IGNORE_CACHE_CTX_KEY, tc.ignoreCache)

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

func runTestWrapObject(t *testing.T, cache cachefunk.Cache) {
	cache.SetConfig(&cachefunk.CacheFunkConfig{
		Configs: map[string]*cachefunk.KeyConfig{
			"helloWorld": {TTL: 5, TTLJitter: 1},
			// "helloWorld2": {TTL: 5, UseCompression: true},
		},
	})

	helloCounter := 0
	type HelloWorldResult struct {
		Result string
		Params *HelloWorldParams
	}

	helloWorld := func(ignoreCache bool, params *HelloWorldParams) (*HelloWorldResult, error) {
		helloCounter += 1
		s := fmt.Sprintf("Hello %s, you are %d", params.Name, params.Age)
		return &HelloWorldResult{
			Params: params,
			Result: s,
		}, nil
	}

	HelloWorld := cachefunk.WrapObject(cache, "helloWorld", helloWorld)

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

	if cacheEntries := cache.EntryCount(); cacheEntries != 3 {
		t.Fatalf("expected %d cached values got %d", 3, cacheEntries)
	}

	HelloWorld2 := cachefunk.WrapObject(cache, "helloWorld2", helloWorld)

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

	// test compression with bad gzipped value
	params := &HelloWorldParams{"Bob", 43}
	paramsRendered, _ := json.Marshal(params)
	doctoredResult := &HelloWorldResult{
		Result: "something else",
		Params: nil,
	}
	raw, _ := json.Marshal(doctoredResult)
	cache.SetRaw("helloWorld2", string(paramsRendered), raw, time.Time{}, true)
	result, err := HelloWorld2(false, params)
	if err != nil {
		t.Errorf("testing gzip bad decompression: %s", err)
	} else if result.Result == "something else" {
		t.Errorf("got unexpected poisoned value")
	}

	cache.Clear()

	if cacheEntries := cache.EntryCount(); cacheEntries != 0 {
		t.Fatalf("expected %d cached values after clear got %d", 0, cacheEntries)
	}
}

func runTestWrapObjectWithContext(t *testing.T, cache cachefunk.Cache) {
	cache.SetConfig(&cachefunk.CacheFunkConfig{
		Defaults: &cachefunk.KeyConfig{TTL: 5, TTLJitter: 1},
		Configs: map[string]*cachefunk.KeyConfig{
			"helloWorld": {TTL: 5, TTLJitter: 1},
		},
	})

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

	HelloWorld := cachefunk.WrapObjectWithContext(cache, "helloWorld", helloWorld)

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
		ctx := context.WithValue(context.TODO(), cachefunk.DEFAULT_IGNORE_CACHE_CTX_KEY, tc.ignoreCache)

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

	if cacheEntries := cache.EntryCount(); cacheEntries != 3 {
		t.Fatalf("expected %d cached values got %d", 3, cacheEntries)
	}

	HelloWorld2 := cachefunk.WrapObjectWithContext(cache, "helloWorld2", helloWorld)

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
		ctx := context.WithValue(context.TODO(), cachefunk.DEFAULT_IGNORE_CACHE_CTX_KEY, tc.ignoreCache)

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

	if cacheEntries := cache.EntryCount(); cacheEntries != 0 {
		t.Fatalf("expected %d cached values after clear got %d", 0, cacheEntries)
	}
}
