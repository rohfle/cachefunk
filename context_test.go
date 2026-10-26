package cachefunk

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// probeStorage wraps InMemoryStorage and runs optional hooks, so a test can block
// a read or a write until its context is done.
type probeStorage struct {
	*InMemoryStorage
	onGet func(ctx context.Context) error
	onSet func(ctx context.Context) error
}

func (s *probeStorage) Get(ctx context.Context, key string, config *KeyConfig, params string, expireBeforeTime time.Time) ([]byte, error) {
	if s.onGet != nil {
		if err := s.onGet(ctx); err != nil {
			return nil, err
		}
	}
	return s.InMemoryStorage.Get(ctx, key, config, params, expireBeforeTime)
}

func (s *probeStorage) Set(ctx context.Context, key string, config *KeyConfig, params string, value []byte, timestamp time.Time) error {
	if s.onSet != nil {
		if err := s.onSet(ctx); err != nil {
			return err
		}
	}
	return s.InMemoryStorage.Set(ctx, key, config, params, value, timestamp)
}

func newProbeCache(storage CacheStorage) *CacheFunk {
	return &CacheFunk{
		Config:  Config{DefaultKey: {TTL: 60, TTLJitter: 0}},
		Storage: storage,
	}
}

func waitForSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	waitFor(t, what, func() bool {
		select {
		case <-ch:
			return true
		default:
			return false
		}
	})
}

// TestCancelledWaiterReturnsWhileFlightRuns checks that a caller who gives up
// stops waiting without cancelling the resolver or the write behind it.
func TestCancelledWaiterReturnsWhileFlightRuns(t *testing.T) {
	cache := newProbeCache(&probeStorage{InMemoryStorage: NewInMemoryStorage()})
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	resolver := func(ctx context.Context, params string) (string, error) {
		once.Do(func() { close(started) })
		select {
		case <-release:
			return "value", nil
		case <-time.After(10 * time.Second):
			return "", errors.New("resolver was never released")
		}
	}

	leaderDone := make(chan error, 1)
	go func() {
		_, err := CacheWithContext(cache, "k", resolver, context.Background(), "p")
		leaderDone <- err
	}()
	waitForSignal(t, started, "the resolver to start")

	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	waiterDone := make(chan error, 1)
	go func() {
		_, err := CacheWithContext(cache, "k", resolver, waiterCtx, "p")
		waiterDone <- err
	}()
	cancelWaiter()

	select {
	case err := <-waiterDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter: got %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled waiter did not return while the flight was still running")
	}

	close(release)
	select {
	case err := <-leaderDone:
		if err != nil {
			t.Fatalf("leader: %s", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("leader did not finish")
	}
	if count, err := cache.EntryCount(context.Background()); err != nil || count != 1 {
		t.Fatalf("expected the write to land, got %d entries (%v)", count, err)
	}
}

// TestDeadlineDuringReadIsNotAFault checks that a caller whose deadline expires
// during a storage read gets its own context error, with no warning and no
// fault count, and that no resolver runs for it.
func TestDeadlineDuringReadIsNotAFault(t *testing.T) {
	storage := &probeStorage{InMemoryStorage: NewInMemoryStorage()}
	storage.onGet = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	cache := newProbeCache(storage)

	var calls atomic.Int64
	resolver := func(ctx context.Context, params string) (string, error) {
		calls.Add(1)
		return "value", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if _, err := CacheWithContext(cache, "k", resolver, ctx, "p"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want context.DeadlineExceeded", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("resolver ran %d times for a caller that had given up", calls.Load())
	}
	if faults := cache.FaultCounts(); faults.ReadErrors != 0 {
		t.Fatalf("a caller deadline was counted as a read fault: %+v", faults)
	}
}

// TestCancelledCallerStillGetsHit pins the documented behaviour that a caller who
// is already gone is still served from a live entry, without starting a resolver.
func TestCancelledCallerStillGetsHit(t *testing.T) {
	cache := newProbeCache(NewInMemoryStorage())
	config := cache.Config.Get("k")
	params, err := config.GetParamCodec().Marshal("p")
	if err != nil {
		t.Fatalf("marshal params: %s", err)
	}
	if err := cache.Set(context.Background(), "k", config, params, "cached"); err != nil {
		t.Fatalf("Set: %s", err)
	}

	var calls atomic.Int64
	resolver := func(ctx context.Context, p string) (string, error) {
		calls.Add(1)
		return "resolved", nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	value, err := CacheWithContext(cache, "k", resolver, ctx, "p")
	if err != nil || value != "cached" {
		t.Fatalf("got %q, %v; want the cached value", value, err)
	}
	if calls.Load() != 0 {
		t.Fatalf("resolver ran for a caller that had given up")
	}
}

// TestStorageTimeoutOnWrite checks that a hung write is abandoned after
// StorageTimeout, counted as a write fault, and does not fail the call.
func TestStorageTimeoutOnWrite(t *testing.T) {
	storage := &probeStorage{InMemoryStorage: NewInMemoryStorage()}
	storage.onSet = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	cache := newProbeCache(storage)
	cache.StorageTimeout = 25 * time.Millisecond

	start := time.Now()
	value, err := Cache(cache, "k", func(p string) (string, error) { return "resolved", nil }, "p")
	elapsed := time.Since(start)

	if err != nil || value != "resolved" {
		t.Fatalf("got %q, %v; want the resolved value", value, err)
	}
	if elapsed < 20*time.Millisecond {
		t.Fatalf("returned after %s, before the storage timeout could fire", elapsed)
	}
	if faults := cache.FaultCounts(); faults.WriteErrors != 1 {
		t.Fatalf("expected 1 write fault, got %+v", faults)
	}
}

// TestStorageTimeoutOnRead checks that a caller's own read is bounded when its
// context has no deadline, is counted as a read fault, and does not fail the
// call.
func TestStorageTimeoutOnRead(t *testing.T) {
	storage := &probeStorage{InMemoryStorage: NewInMemoryStorage()}
	var gets atomic.Int64
	storage.onGet = func(ctx context.Context) error {
		if gets.Add(1) == 1 {
			<-ctx.Done() // the caller's fast-path read must be bounded
			return ctx.Err()
		}
		return ErrEntryNotFound
	}
	cache := newProbeCache(storage)
	cache.StorageTimeout = 25 * time.Millisecond

	value, err := Cache(cache, "k", func(p string) (string, error) { return "resolved", nil }, "p")
	if err != nil || value != "resolved" {
		t.Fatalf("got %q, %v; want the resolved value", value, err)
	}
	if faults := cache.FaultCounts(); faults.ReadErrors != 1 {
		t.Fatalf("expected 1 read fault, got %+v", faults)
	}
}

// TestStorageTimeoutOnFlightRead checks that the flight's re-read is bounded, so
// a storage that hangs after the fast-path miss does not block the waiters: the
// resolver still runs and the call succeeds.
func TestStorageTimeoutOnFlightRead(t *testing.T) {
	storage := &probeStorage{InMemoryStorage: NewInMemoryStorage()}
	var gets atomic.Int64
	storage.onGet = func(ctx context.Context) error {
		if gets.Add(1) == 1 {
			return ErrEntryNotFound
		}
		<-ctx.Done() // the flight's re-read must be bounded
		return ctx.Err()
	}
	cache := newProbeCache(storage)
	cache.StorageTimeout = 25 * time.Millisecond

	var calls atomic.Int64
	value, err := Cache(cache, "k", func(p string) (string, error) {
		calls.Add(1)
		return "resolved", nil
	}, "p")
	if err != nil || value != "resolved" {
		t.Fatalf("got %q, %v; want the resolved value", value, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("resolver ran %d times, want 1", calls.Load())
	}
	if faults := cache.FaultCounts(); faults.ReadErrors != 1 {
		t.Fatalf("expected 1 read fault, got %+v", faults)
	}
}

// TestReadContextBoundsCancelledCaller checks that a caller that is already gone
// gets a detached read that stays bounded even when StorageTimeout disables
// bounds everywhere else.
func TestReadContextBoundsCancelledCaller(t *testing.T) {
	cache := newProbeCache(NewInMemoryStorage())
	cache.StorageTimeout = -1

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	readCtx, release := cache.readContext(ctx)
	defer release()

	if readCtx.Err() != nil {
		t.Fatalf("read context is already done: %v", readCtx.Err())
	}
	deadline, ok := readCtx.Deadline()
	if !ok {
		t.Fatal("read context for a caller that is gone has no deadline")
	}
	if remaining := time.Until(deadline); remaining < DefaultStorageTimeout-time.Second {
		t.Fatalf("deadline is %s away, want about %s", remaining, DefaultStorageTimeout)
	}
}

// TestLeaderCancelsWaiterContinues checks that the flight outlives the caller
// that started it.
func TestLeaderCancelsWaiterContinues(t *testing.T) {
	cache := newProbeCache(&probeStorage{InMemoryStorage: NewInMemoryStorage()})
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	resolver := func(ctx context.Context, params string) (string, error) {
		once.Do(func() { close(started) })
		select {
		case <-release:
			return "value", nil
		case <-time.After(10 * time.Second):
			return "", errors.New("resolver was never released")
		}
	}

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderDone := make(chan error, 1)
	go func() {
		_, err := CacheWithContext(cache, "k", resolver, leaderCtx, "p")
		leaderDone <- err
	}()
	waitForSignal(t, started, "the resolver to start")

	waiterDone := make(chan struct{})
	var waiterValue string
	var waiterErr error
	go func() {
		waiterValue, waiterErr = CacheWithContext(cache, "k", resolver, context.Background(), "p")
		close(waiterDone)
	}()

	cancelLeader()
	select {
	case err := <-leaderDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("leader: got %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled leader did not return")
	}

	close(release)
	select {
	case <-waiterDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the remaining waiter did not receive the flight's result")
	}
	if waiterErr != nil || waiterValue != "value" {
		t.Fatalf("waiter got %q, %v; want the resolved value", waiterValue, waiterErr)
	}
	if count, err := cache.EntryCount(context.Background()); err != nil || count != 1 {
		t.Fatalf("expected the write to land after the leader left, got %d entries (%v)", count, err)
	}
}

// TestFlightContextKeepsValuesDropsCancellation pins the context passed to
// storage inside a flight: leader values travel, cancellation does not.
func TestFlightContextKeepsValuesDropsCancellation(t *testing.T) {
	storage := &probeStorage{InMemoryStorage: NewInMemoryStorage()}
	var (
		mutex     sync.Mutex
		flightCtx context.Context
		gets      atomic.Int64
	)
	storage.onGet = func(ctx context.Context) error {
		// the first read is the caller's fast path, the second is the flight's
		if gets.Add(1) >= 2 {
			mutex.Lock()
			flightCtx = ctx
			mutex.Unlock()
		}
		return nil
	}
	cache := newProbeCache(storage)
	cache.StorageTimeout = -1 // unbounded, so storage sees the flight context itself

	type leaderKey struct{}
	leaderCtx, cancelLeader := context.WithCancel(context.WithValue(context.Background(), leaderKey{}, "leader"))
	defer cancelLeader()

	if _, err := CacheWithContext(cache, "k", func(ctx context.Context, p string) (string, error) {
		return "value", nil
	}, leaderCtx, "p"); err != nil {
		t.Fatalf("CacheWithContext: %s", err)
	}

	mutex.Lock()
	got := flightCtx
	mutex.Unlock()
	if got == nil {
		t.Fatal("the flight did not read storage")
	}
	if value := got.Value(leaderKey{}); value != "leader" {
		t.Fatalf("flight context lost the leader's values, got %v", value)
	}
	if got.Done() != nil {
		t.Fatal("flight context is cancellable; it must outlive the leader")
	}
	if _, hasDeadline := got.Deadline(); hasDeadline {
		t.Fatal("flight context has a deadline although StorageTimeout is negative")
	}
}

// cancelAfterCtx reports cancellation after a fixed number of Err calls, so a
// test can interrupt a walk part way through without sleeping.
type cancelAfterCtx struct {
	context.Context
	remaining atomic.Int64
}

func (c *cancelAfterCtx) Err() error {
	if c.remaining.Add(-1) < 0 {
		return context.Canceled
	}
	return nil
}

// TestCleanupCancelledMidWalk checks that Cleanup stops promptly, reports the
// cancellation with the key it reached, and leaves the rest for the next call.
func TestCleanupCancelledMidWalk(t *testing.T) {
	// a flat layout, so the cancellation budget is spent on entry files rather
	// than on the hashed pather's directories
	storage, err := NewDiskStorage(t.TempDir(), func(params string) []string {
		return []string{params}
	})
	if err != nil {
		t.Fatalf("NewDiskStorage: %s", err)
	}
	cache := newProbeCache(storage)
	ctx := context.Background()
	config := cache.Config.Get("k")

	const entries = 50
	written := time.Now().UTC().Add(-time.Hour)
	for i := range entries {
		params := strings.Repeat("p", i+1)
		if err := storage.Set(ctx, "k", config, params, []byte("value"), written); err != nil {
			t.Fatalf("Set %d: %s", i, err)
		}
	}

	// enough Err calls to delete some files, not all of them
	interrupted := &cancelAfterCtx{Context: ctx}
	interrupted.remaining.Store(12)
	err = cache.Cleanup(interrupted, true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Cleanup: got %v, want context.Canceled", err)
	}
	if !strings.Contains(err.Error(), "cleanup cancelled") {
		t.Fatalf("Cleanup error should name the cancellation, got %q", err)
	}
	remaining, countErr := cache.EntryCount(ctx)
	if countErr != nil {
		t.Fatalf("EntryCount: %s", countErr)
	}
	if remaining == 0 || remaining == entries {
		t.Fatalf("Cleanup removed %d of %d entries; want some, not all", entries-remaining, entries)
	}

	if err := cache.Cleanup(ctx, true); err != nil {
		t.Fatalf("Cleanup after cancellation: %s", err)
	}
	if remaining, err := cache.EntryCount(ctx); err != nil || remaining != 0 {
		t.Fatalf("expected all entries gone after a second Cleanup, got %d (%v)", remaining, err)
	}
}
