package cachefunk_test

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/rohfle/cachefunk"
	"github.com/rohfle/cachefunk/storagetest"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestInMemoryStorageConformance(t *testing.T) {
	storagetest.Run(t, func(t *testing.T) (cachefunk.CacheStorage, func()) {
		return cachefunk.NewInMemoryStorage(), nil
	})
}

func TestDiskStorageConformance(t *testing.T) {
	storagetest.Run(t, func(t *testing.T) (cachefunk.CacheStorage, func()) {
		storage, err := cachefunk.NewDiskStorage(t.TempDir(), nil)
		if err != nil {
			t.Fatalf("NewDiskStorage: %s", err)
		}
		return storage, nil
	})
}

var conformanceDB atomic.Int64

func TestGORMStorageConformance(t *testing.T) {
	storagetest.Run(t, func(t *testing.T) (cachefunk.CacheStorage, func()) {
		// a distinct in-memory database per call, so each subtest starts empty
		dsn := fmt.Sprintf("file:conformance_%d?mode=memory&cache=shared", conformanceDB.Add(1))
		db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
		if err != nil {
			t.Skipf("sqlite driver unavailable (needs cgo): %s", err)
		}
		storage, err := cachefunk.NewGORMStorage(db)
		if err != nil {
			t.Fatalf("NewGORMStorage: %s", err)
		}
		return storage, nil
	})
}
