// Package storagetest checks that a [cachefunk.CacheStorage] implementation
// honours the contract CacheFunk relies on.
//
// Backends should run it from their own tests, and so should third-party
// implementations, since the contract is wide enough that a subtle violation
// only shows up as a cache miss at runtime.
package storagetest

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rohfle/cachefunk"
)

// Run exercises storage, which open must return empty for each call along with a
// cleanup function. It covers the round trip, expiry, compression mismatch,
// keys, counts, cleanup, clearing and cancellation.
func Run(t *testing.T, open func(t *testing.T) (cachefunk.CacheStorage, func())) {
	t.Helper()

	const (
		key    = "alpha"
		params = `{"p":1}`
	)
	value := []byte("encoded value")
	written := time.Now().UTC().Add(-time.Hour)
	cfg := &cachefunk.KeyConfig{TTL: 60, BodyCompression: cachefunk.ZstdCompression}
	fallbackCfg := &cachefunk.KeyConfig{TTL: 60, BodyCompression: cachefunk.ZstdCompression, FallbackToExpired: true}
	plainCfg := &cachefunk.KeyConfig{TTL: 60, BodyCompression: cachefunk.NoCompression}
	ctx := context.Background()

	newStorage := func(t *testing.T) cachefunk.CacheStorage {
		t.Helper()
		storage, cleanup := open(t)
		if cleanup != nil {
			t.Cleanup(cleanup)
		}
		return storage
	}

	t.Run("round trip", func(t *testing.T) {
		storage := newStorage(t)
		if err := storage.Set(ctx, key, cfg, params, value, time.Now().UTC()); err != nil {
			t.Fatalf("Set: %s", err)
		}
		got, err := storage.Get(ctx, key, cfg, params, cfg.GetExpireTime(time.Now().UTC()))
		if err != nil {
			t.Fatalf("Get: %s", err)
		}
		if !bytes.Equal(got, value) {
			t.Fatalf("Get returned %q, want %q", got, value)
		}
	})

	t.Run("missing entry", func(t *testing.T) {
		storage := newStorage(t)
		if _, err := storage.Get(ctx, key, cfg, params, cfg.GetExpireTime(time.Now().UTC())); !errors.Is(err, cachefunk.ErrEntryNotFound) {
			t.Fatalf("Get on empty storage: got %v, want ErrEntryNotFound", err)
		}
	})

	t.Run("expired entry", func(t *testing.T) {
		storage := newStorage(t)
		if err := storage.Set(ctx, key, cfg, params, value, written); err != nil {
			t.Fatalf("Set: %s", err)
		}
		expireBeforeTime := cfg.GetExpireTime(time.Now().UTC())
		if _, err := storage.Get(ctx, key, cfg, params, expireBeforeTime); !errors.Is(err, cachefunk.ErrEntryExpired) {
			t.Fatalf("Get of expired entry: got %v, want ErrEntryExpired", err)
		}
		got, err := storage.Get(ctx, key, fallbackCfg, params, expireBeforeTime)
		if !errors.Is(err, cachefunk.ErrEntryExpired) {
			t.Fatalf("Get of expired entry with FallbackToExpired: got %v, want ErrEntryExpired", err)
		}
		if !bytes.Equal(got, value) {
			t.Fatalf("Get of expired entry with FallbackToExpired returned %q, want %q", got, value)
		}
	})

	t.Run("compression mismatch", func(t *testing.T) {
		storage := newStorage(t)
		if err := storage.Set(ctx, key, cfg, params, value, time.Now().UTC()); err != nil {
			t.Fatalf("Set: %s", err)
		}
		if _, err := storage.Get(ctx, key, plainCfg, params, plainCfg.GetExpireTime(time.Now().UTC())); !errors.Is(err, cachefunk.ErrEntryNotFound) {
			t.Fatalf("Get with another compression: got %v, want ErrEntryNotFound", err)
		}
	})

	t.Run("keys and counts", func(t *testing.T) {
		storage := newStorage(t)
		if err := storage.Set(ctx, key, cfg, params, value, written); err != nil {
			t.Fatalf("Set expired: %s", err)
		}
		if err := storage.Set(ctx, key, cfg, `{"p":2}`, value, time.Now().UTC()); err != nil {
			t.Fatalf("Set fresh: %s", err)
		}
		keys, err := storage.Keys(ctx)
		if err != nil {
			t.Fatalf("Keys: %s", err)
		}
		if len(keys) != 1 || keys[0] != key {
			t.Fatalf("Keys returned %v, want [%q]", keys, key)
		}
		count, err := storage.EntryCount(ctx)
		if err != nil || count != 2 {
			t.Fatalf("EntryCount returned %d, %v; want 2, nil", count, err)
		}
		expired, err := storage.ExpiredEntryCount(ctx, key, cfg, cfg.GetExpireTime(time.Now().UTC()))
		if err != nil || expired != 1 {
			t.Fatalf("ExpiredEntryCount returned %d, %v; want 1, nil", expired, err)
		}
	})

	t.Run("cleanup", func(t *testing.T) {
		storage := newStorage(t)
		if err := storage.Set(ctx, key, cfg, params, value, written); err != nil {
			t.Fatalf("Set: %s", err)
		}
		if err := storage.Cleanup(ctx, key, cfg, cfg.GetExpireTime(time.Now().UTC())); err != nil {
			t.Fatalf("Cleanup: %s", err)
		}
		count, err := storage.EntryCount(ctx)
		if err != nil || count != 0 {
			t.Fatalf("EntryCount after Cleanup returned %d, %v; want 0, nil", count, err)
		}
	})

	t.Run("clear", func(t *testing.T) {
		storage := newStorage(t)
		if err := storage.Set(ctx, key, cfg, params, value, time.Now().UTC()); err != nil {
			t.Fatalf("Set: %s", err)
		}
		if err := storage.Clear(ctx); err != nil {
			t.Fatalf("Clear: %s", err)
		}
		count, err := storage.EntryCount(ctx)
		if err != nil || count != 0 {
			t.Fatalf("EntryCount after Clear returned %d, %v; want 0, nil", count, err)
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		storage := newStorage(t)
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		expireBeforeTime := cfg.GetExpireTime(time.Now().UTC())

		if _, err := storage.Get(cancelled, key, cfg, params, expireBeforeTime); !errors.Is(err, context.Canceled) {
			t.Errorf("Get: got %v, want context.Canceled", err)
		}
		if err := storage.Set(cancelled, key, cfg, params, value, time.Now().UTC()); !errors.Is(err, context.Canceled) {
			t.Errorf("Set: got %v, want context.Canceled", err)
		}
		if _, err := storage.Keys(cancelled); !errors.Is(err, context.Canceled) {
			t.Errorf("Keys: got %v, want context.Canceled", err)
		}
		if _, err := storage.EntryCount(cancelled); !errors.Is(err, context.Canceled) {
			t.Errorf("EntryCount: got %v, want context.Canceled", err)
		}
		if _, err := storage.ExpiredEntryCount(cancelled, key, cfg, expireBeforeTime); !errors.Is(err, context.Canceled) {
			t.Errorf("ExpiredEntryCount: got %v, want context.Canceled", err)
		}
		if err := storage.Clear(cancelled); !errors.Is(err, context.Canceled) {
			t.Errorf("Clear: got %v, want context.Canceled", err)
		}
		if err := storage.Cleanup(cancelled, key, cfg, expireBeforeTime); !errors.Is(err, context.Canceled) {
			t.Errorf("Cleanup: got %v, want context.Canceled", err)
		}
	})
}
