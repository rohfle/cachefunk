package cachefunk

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor polls a condition until it holds or the deadline passes
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// singleflightConfig caches the given keys with a TTL long enough that the test
// itself decides what is in the cache and what has to be retrieved
func singleflightConfig(keys ...string) Config {
	config := Config{}
	for _, key := range keys {
		config[key] = &KeyConfig{TTL: 60, TTLJitter: 0, BodyCodec: StringCodec, BodyCompression: NoCompression}
	}
	return config
}

// runTestSingleflightCoalesces checks that concurrent retrievals of the same key
// and params reach the resolver once, whatever mix of cache-reading and
// ignoreCache callers they are, and that different params is its own retrieval.
func runTestSingleflightCoalesces(t *testing.T, cache *CacheFunk) {
	cache.Config = singleflightConfig("singleflight_Coalesce")

	var calls atomic.Int64
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	retrieve := func(ignoreCache bool, params string) (string, error) {
		calls.Add(1)
		startOnce.Do(func() { close(started) })
		<-release // hold the retrieval open so the other callers can join it
		return "value for " + params, nil
	}
	Fetch := WrapWithIgnore(cache, "singleflight_Coalesce", retrieve)

	// the ignoreCache caller goes first, so the retrieval it starts is the one
	// the cache-reading callers below join
	var bypassResult string
	var bypassErr error
	bypassDone := make(chan struct{})
	go func() {
		defer close(bypassDone)
		bypassResult, bypassErr = Fetch(true, "same")
	}()
	<-started

	const callers = 8
	results := make([]string, callers)
	errs := make([]error, callers)
	var entered atomic.Int64

	// release every caller at the same moment so they overlap
	barrier := make(chan struct{})
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			entered.Add(1)
			results[i], errs[i] = Fetch(false, "same")
		}()
	}

	close(barrier)
	// let the cache-reading callers reach the join while the retrieval is still
	// in flight: joining it, rather than finding a finished one, is the behaviour
	// under test
	waitFor(t, "all callers to reach the wrapped function", func() bool {
		return entered.Load() == callers
	})
	time.Sleep(20 * time.Millisecond)

	close(release)
	wg.Wait()
	<-bypassDone

	if got := calls.Load(); got != 1 {
		t.Errorf("expected 1 resolver call shared by %d callers but got %d", callers+1, got)
	}
	for i := range callers {
		if errs[i] != nil {
			t.Errorf("caller %d returned error %v", i, errs[i])
		}
		if results[i] != "value for same" {
			t.Errorf("caller %d returned %q", i, results[i])
		}
	}
	if bypassErr != nil || bypassResult != "value for same" {
		t.Errorf("ignoreCache caller returned %q, %v", bypassResult, bypassErr)
	}

	// params are part of the flight key: different params is another retrieval
	if _, err := Fetch(false, "other"); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("expected a second call for different params but got %d", got)
	}

	// the shared retrieval stored its result, so this is now a cache hit
	if value, err := Fetch(false, "same"); err != nil || value != "value for same" {
		t.Errorf("expected the cached value but got %q, %v", value, err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("expected the cached value but the resolver was called %d times", got)
	}
}

// runTestSingleflightPanicIsReturnedAsError checks that a resolver panic does not
// take the process down with it: the flight reports it as an error to every
// caller waiting on it, and the next call resolves normally.
func runTestSingleflightPanicIsReturnedAsError(t *testing.T, cache *CacheFunk) {
	cache.Config = singleflightConfig("singleflight_Panic")

	var calls atomic.Int64
	started := make(chan struct{})
	explode := make(chan struct{})
	var startOnce sync.Once
	retrieve := func(ignoreCache bool, params string) (string, error) {
		if calls.Add(1) == 1 {
			startOnce.Do(func() { close(started) })
			<-explode
			panic("resolver exploded")
		}
		return "recovered", nil
	}
	Fetch := WrapWithIgnore(cache, "singleflight_Panic", retrieve)

	leaderErr := make(chan error, 1)
	go func() {
		_, err := Fetch(false, "p")
		leaderErr <- err
	}()
	<-started

	// a second caller joins the flight, so the panic has to reach it too
	joinerErr := make(chan error, 1)
	go func() {
		_, err := Fetch(false, "p")
		joinerErr <- err
	}()
	time.Sleep(20 * time.Millisecond)

	close(explode)

	for _, tc := range []struct {
		what string
		err  error
	}{{"leading", <-leaderErr}, {"joining", <-joinerErr}} {
		if tc.err == nil {
			t.Errorf("the %s caller returned no error for a panicking resolver", tc.what)
		}
	}
	// both callers shared the one flight: a caller that missed the join would
	// have called the resolver again and returned "recovered" instead
	if got := calls.Load(); got != 1 {
		t.Errorf("expected 1 resolver call shared by both callers but got %d", got)
	}

	// the panic left nothing cached and the flight is gone, so this retrieves
	if value, err := Fetch(false, "p"); err != nil || value != "recovered" {
		t.Errorf("expected the flight to recover after a panic but got %q, %v", value, err)
	}
}

// runTestSingleflightDoneContext checks that a caller whose context is already
// done does not reach the resolver, but is still served by a fresh cache entry.
func runTestSingleflightDoneContext(t *testing.T, cache *CacheFunk) {
	cache.Config = singleflightConfig("singleflight_DoneContext")

	var calls atomic.Int64
	Fetch := WrapWithContext(cache, "singleflight_DoneContext", func(ctx context.Context, params string) (string, error) {
		calls.Add(1)
		return "value", nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	value, err := Fetch(ctx, "p")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled but got %q, %v", value, err)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("expected no resolver call for a done context but got %d", got)
	}

	// the same call with a live context still resolves
	if value, err := Fetch(context.Background(), "p"); err != nil || value != "value" {
		t.Errorf("expected the value with a live context but got %q, %v", value, err)
	}

	// the context check comes after the cache read: a fresh entry is still served
	value, err = Fetch(ctx, "p")
	if err != nil || value != "value" {
		t.Errorf("expected the cached value for a done context but got %q, %v", value, err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("expected the fresh entry to be served without a resolver call but got %d calls", got)
	}
}

// runTestSingleflightCallerGivesUp checks that a caller whose context is done
// stops waiting without cancelling the retrieval other callers are waiting on.
func runTestSingleflightCallerGivesUp(t *testing.T, cache *CacheFunk) {
	cache.Config = singleflightConfig("singleflight_GiveUp")

	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	var startOnce sync.Once
	retrieve := func(ctx context.Context, params string) (string, error) {
		calls.Add(1)
		startOnce.Do(func() { close(started) })
		<-release
		if err := ctx.Err(); err != nil {
			// the shared retrieval must outlive the caller that gave up on it
			return "", fmt.Errorf("the retrieval context was cancelled: %w", err)
		}
		return "value", nil
	}
	Fetch := WrapWithContext(cache, "singleflight_GiveUp", retrieve)

	// the first caller starts the retrieval and then gives up on it
	abandonedCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	abandoned := make(chan error, 1)
	go func() {
		_, err := Fetch(abandonedCtx, "p")
		abandoned <- err
	}()
	<-started

	// a second caller joins the same retrieval with a context of its own
	joined := make(chan string, 1)
	joinedErrs := make(chan error, 1)
	go func() {
		value, err := Fetch(context.Background(), "p")
		joined <- value
		joinedErrs <- err
	}()

	select {
	case err := <-abandoned:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected the giving up caller to return DeadlineExceeded but got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the giving up caller is still waiting for the retrieval to finish")
	}

	close(release)

	if err := <-joinedErrs; err != nil {
		t.Errorf("the joining caller returned error %v", err)
	}
	if value := <-joined; value != "value" {
		t.Errorf("the joining caller returned %q", value)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("expected 1 resolver call but got %d", got)
	}
}

// runTestSingleflightReentrantCall checks that a resolver which calls the cache
// back with the context it was given, for the key+params it is resolving, gets
// ErrReentrantCall instead of waiting on the flight it is part of.
func runTestSingleflightReentrantCall(t *testing.T, cache *CacheFunk) {
	cache.Config = singleflightConfig("singleflight_Reentrant")

	var calls atomic.Int64
	var Fetch func(context.Context, string) (string, error)
	Fetch = WrapWithContext(cache, "singleflight_Reentrant", func(ctx context.Context, params string) (string, error) {
		calls.Add(1)
		return Fetch(ctx, params)
	})

	// a deadline so that a missing re-entrancy guard fails this test in seconds
	// rather than blocking until the test binary timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	value, err := Fetch(ctx, "p")
	if !errors.Is(err, ErrReentrantCall) {
		t.Errorf("expected ErrReentrantCall from the nested call but got %q, %v", value, err)
	}
	// the nested call was rejected before it could reach the resolver
	if got := calls.Load(); got != 1 {
		t.Errorf("expected the nested call not to reach the resolver but it was called %d times", got)
	}
}

// runTestSingleflightNestedCalls checks that a resolver can still call the cache
// with its own context for another key, or for the same key with different
// params: only the key+params being resolved is re-entrant.
func runTestSingleflightNestedCalls(t *testing.T, cache *CacheFunk) {
	cache.Config = singleflightConfig("singleflight_Outer", "singleflight_Inner")

	Inner := WrapWithContext(cache, "singleflight_Inner", func(ctx context.Context, params string) (string, error) {
		return "inner " + params, nil
	})
	var Outer func(context.Context, string) (string, error)
	Outer = WrapWithContext(cache, "singleflight_Outer", func(ctx context.Context, params string) (string, error) {
		if params != "p" {
			return "outer " + params, nil // nested call, no further nesting
		}
		other, err := Outer(ctx, "other") // same key, different params
		if err != nil {
			return "", err
		}
		inner, err := Inner(ctx, params) // different key, same params
		if err != nil {
			return "", err
		}
		return other + " + " + inner, nil
	})

	// a deadline so that a marker which wrongly rejects nested calls fails this
	// test in seconds rather than blocking until the test binary timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	value, err := Outer(ctx, "p")
	if err != nil {
		t.Fatalf("expected the nested calls to resolve but got %q, %v", value, err)
	}
	if want := "outer other + inner p"; value != want {
		t.Errorf("expected %q but got %q", want, value)
	}
}
