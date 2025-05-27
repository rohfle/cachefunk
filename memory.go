package cachefunk

import (
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

type InMemoryStorageEntry struct {
	Data            []byte
	Timestamp       time.Time
	CompressionType string
}

// InMemoryStorage stores cache entries in a map
type InMemoryStorage struct {
	store map[string]*InMemoryStorageEntry
	mutex sync.RWMutex
}

func NewInMemoryStorage() *InMemoryStorage {
	cache := InMemoryStorage{
		store: make(map[string]*InMemoryStorageEntry, 0),
	}
	return &cache
}

func (c *InMemoryStorage) Get(key string, config *KeyConfig, params string, expireTime time.Time) ([]byte, error) {
	fullKey := key + ":" + params
	c.mutex.RLock()
	value, found := c.store[fullKey]
	c.mutex.RUnlock()
	if !found {
		return nil, ErrEntryNotFound
	}

	if value.CompressionType != config.GetBodyCompression().String() {
		// yes there is an entry but it has the wrong compression type
		// so it is the same as not found
		return nil, ErrEntryNotFound
	}

	// check if cached value has expired
	if expireTime.After(value.Timestamp) {
		// item has expired but DO NOT REMOVE THE ITEM
		// if FallbackToExpired option set expired value
		// will be used if retrieve function fails
		return nil, ErrEntryExpired
	}

	return value.Data, nil
}

func (c *InMemoryStorage) Set(key string, config *KeyConfig, params string, value []byte, timestamp time.Time) error {
	fullKey := key + ":" + params

	entry := &InMemoryStorageEntry{
		Data:            value,
		Timestamp:       timestamp,
		CompressionType: config.GetBodyCompression().String(),
	}

	c.mutex.Lock()
	c.store[fullKey] = entry
	c.mutex.Unlock()
	return nil
}

func (c *InMemoryStorage) Clear() error {
	c.mutex.Lock()
	c.store = make(map[string]*InMemoryStorageEntry, 0)
	c.mutex.Unlock()
	return nil
}

func (c *InMemoryStorage) Cleanup(key string, config *KeyConfig, expireTime time.Time) error {
	c.mutex.Lock()
	for fullkey, value := range c.store {
		if !strings.HasPrefix(fullkey, key+":") {
			continue
		}
		if expireTime.After(value.Timestamp) {
			// its safe to delete from maps during a range loop
			delete(c.store, fullkey)
		}
	}
	c.mutex.Unlock()
	return nil
}

func (c *InMemoryStorage) EntryCount() (int64, error) {
	c.mutex.RLock()
	count := len(c.store)
	c.mutex.RUnlock()
	return int64(count), nil
}

func (c *InMemoryStorage) ExpiredEntryCount(key string, config *KeyConfig, expireTime time.Time) (int64, error) {
	var count int64 = 0
	c.mutex.RLock()
	for fullkey, value := range c.store {
		if !strings.HasPrefix(fullkey, key+":") {
			continue
		}
		if expireTime.After(value.Timestamp) {
			count += 1
		}
	}
	c.mutex.RUnlock()
	return count, nil
}

func (c *InMemoryStorage) Dump(n int64) {
	var count int64
	c.mutex.RLock()
	for fullkey, value := range c.store {
		if count >= n {
			fmt.Println("...")
			break
		}
		count += 1
		fmt.Printf("fullkey=%s timestamp=%s compression_type=%s data=\n", fullkey, value.Timestamp, value.CompressionType)
		fmt.Println(hex.Dump(value.Data))
	}
	c.mutex.RUnlock()
	if count == 0 {
		fmt.Println("No entries")
	}
}
