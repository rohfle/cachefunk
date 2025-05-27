package cachefunk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func runTestCachePoisoning(t *testing.T, cache *CacheFunk) {
	cache.Config = &Config{
		Configs: map[string]*KeyConfig{
			"bad":  {TTL: 1},
			"good": {TTL: 1},
		},
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

	BadFunction := Wrap(cache, "bad", badFunction)
	GoodFunction := Wrap(cache, "good", goodFunction)

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
	ctx := context.WithValue(context.TODO(), DefaultIgnoreCacheCtxKey, true)

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
	cache.Config = &Config{
		Configs: map[string]*KeyConfig{
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
		},
	}

	callCount := 0
	noop := func(ignoreCache bool, params *HelloWorldParams) ([]byte, error) {
		callCount += 1
		return []byte{}, nil
	}

	CacheTTL := Wrap(cache, "noop_CacheTTL", noop)
	NoCache := Wrap(cache, "noop_NoCache", noop)
	CacheTTLWithJitter := Wrap(cache, "noop_CacheTTLWithJitter", noop)
	CacheTTLNegative := Wrap(cache, "noop_CacheTTLNegative", noop)
	CacheTTLHuge := Wrap(cache, "noop_CacheTTLHuge", noop)
	CacheTTLForever := Wrap(cache, "noop_CacheTTLForever", noop)

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

		cache.Cleanup()

		if t.Failed() {
			return
		}
	}

}

func runTestCacheMismatchCompressionType(t *testing.T, cache *CacheFunk, expireAllEntries func()) {
	cache.Config = &Config{
		Configs: map[string]*KeyConfig{
			"hello_CacheTTL": {TTL: 1, TTLJitter: 0, FallbackToExpired: true, BodyCompression: GzipCompression},
		},
	}

	callCount := 0
	helloRaw := func(ignoreCache bool, params *HelloWorldParams) (string, error) {
		callCount += 1
		return "hello world", nil
	}

	CacheTTL := Wrap(cache, "hello_CacheTTL", helloRaw)

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

	cache.Config.Configs["hello_CacheTTL"] = &KeyConfig{
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
	cache.Config = &Config{
		Configs: map[string]*KeyConfig{
			"hello_CacheTTL": {TTL: 1, TTLJitter: 0, FallbackToExpired: true},
		},
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

	CacheTTL := Wrap(cache, "hello_CacheTTL", helloRaw)

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
	// Check entries expireTime are after now
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

	cache.Cleanup()
	count, err = cache.EntryCount()
	if err != nil {
		t.Fatal("expected 0 cache entries after cache.Cleanup() but got error", err)
	}
	if count != 0 {
		t.Fatal("expected 0 cache entries after cache.Cleanup() but got", count)
	}
}

func runTestCacheFallBackToExpiredWithContext(t *testing.T, cache *CacheFunk, expireAllEntries func()) {
	cache.Config = &Config{
		Configs: map[string]*KeyConfig{
			"hello_CacheTTL": {TTL: 1, TTLJitter: 0, FallbackToExpired: true},
		},
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
	// Check entries expireTime are after now
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

	cache.Cleanup()
	count, err = cache.EntryCount()
	if err != nil {
		t.Fatal("expected 0 cache entries after cache.Cleanup() but got error", err)
	}
	if count != 0 {
		t.Fatal("expected 0 cache entries after cache.Cleanup() but got", count)
	}
}

func runTestCacheFuncErrorsReturned(t *testing.T, cache *CacheFunk) {
	cache.Config = &Config{
		Configs: map[string]*KeyConfig{
			"failWorld": {TTL: 5, TTLJitter: 1},
		},
	}

	failWorld := func(ignoreCache bool, params *HelloWorldParams) (string, error) {
		return "", errors.New("oh no")
	}

	FailWorldString := Wrap(cache, "failWorld", failWorld)

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

	FailWorldJSON := Wrap(cache, "failWorld", failWorld)

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
	cache.Config = &Config{
		Configs: map[string]*KeyConfig{
			"failWorld": {TTL: 5, TTLJitter: 1},
		},
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
	cache.Config = &Config{
		Configs: map[string]*KeyConfig{
			"helloWorld": {TTL: 5, TTLJitter: 1, BodyCompression: NoCompression},
		},
	}

	helloCounter := 0
	helloWorld := func(ignoreCache bool, params *HelloWorldParams) (string, error) {
		helloCounter += 1
		s := fmt.Sprintf("Hello %s, you are %d", params.Name, params.Age)
		return s, nil
	}

	HelloWorld := Wrap(cache, "helloWorld", helloWorld)

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
	cache.Config = &Config{
		Configs: map[string]*KeyConfig{
			"helloWorld": {TTL: 5, TTLJitter: 1},
		},
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
		ctx := context.WithValue(context.TODO(), DefaultIgnoreCacheCtxKey, tc.ignoreCache)

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

func runTestWrapWithObjectResult(t *testing.T, cache *CacheFunk) {
	cache.Config = &Config{
		Configs: map[string]*KeyConfig{
			"helloWorld": {
				TTL:             5,
				TTLJitter:       1,
				BodyCodec:       MsgPackCodec,
				BodyCompression: BrotliCompression,
				ParamCodec:      JSONBase64Params,
			},
			"helloWorld2": {
				TTL:             5,
				BodyCodec:       JSONCodec,
				BodyCompression: GzipCompression,
				ParamCodec:      JSONParams,
			},
		},
	}

	helloCounter := 0
	type HelloWorldResult struct {
		Result string
		Params *HelloWorldParams
	}

	cache.Storage.Dump(1)

	helloWorld := func(ignoreCache bool, params *HelloWorldParams) (*HelloWorldResult, error) {
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

	cache.Storage.Dump(1)

	HelloWorld2 := Wrap(cache, "helloWorld2", helloWorld)

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
		t.Errorf("testing gzip bad decompression: %s", err)
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
	cache.Config = &Config{
		Defaults: &KeyConfig{TTL: 5, TTLJitter: 1},
		Configs: map[string]*KeyConfig{
			"helloWorld": {TTL: 5, TTLJitter: 1},
		},
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
		ctx := context.WithValue(context.TODO(), DefaultIgnoreCacheCtxKey, tc.ignoreCache)

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
		ctx := context.WithValue(context.TODO(), DefaultIgnoreCacheCtxKey, tc.ignoreCache)

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
