package cachefunk

import (
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

type InMemoryStorageEntry struct {
	Data            []byte
	Timestamp       time.Time
	CompressionType string
}

// InMemoryStorage stores cache entries in a map of cache key to map of params
type InMemoryStorage struct {
	store map[string]map[string]*InMemoryStorageEntry
	mutex sync.RWMutex
}

func NewInMemoryStorage() *InMemoryStorage {
	cache := InMemoryStorage{
		store: make(map[string]map[string]*InMemoryStorageEntry),
	}
	return &cache
}

func (c *InMemoryStorage) Get(key string, config *KeyConfig, params string, expireBeforeTime time.Time) ([]byte, error) {
	c.mutex.RLock()
	value, found := c.store[key][params]
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
	if expireBeforeTime.After(value.Timestamp) {
		// item has expired but DO NOT REMOVE THE ITEM
		// if FallbackToExpired option set expired value
		// will be used if retrieve function fails
		return value.Data, ErrEntryExpired
	}

	return value.Data, nil
}

func (c *InMemoryStorage) Set(key string, config *KeyConfig, params string, value []byte, timestamp time.Time) error {
	entry := &InMemoryStorageEntry{
		Data:            value,
		Timestamp:       timestamp,
		CompressionType: config.GetBodyCompression().String(),
	}

	c.mutex.Lock()
	paramsMap, exists := c.store[key]
	if !exists {
		paramsMap = make(map[string]*InMemoryStorageEntry)
		c.store[key] = paramsMap
	}
	paramsMap[params] = entry
	c.mutex.Unlock()
	return nil
}

// Keys returns the distinct cache keys that currently have entries
func (c *InMemoryStorage) Keys() ([]string, error) {
	c.mutex.RLock()
	keys := make([]string, 0, len(c.store))
	for key := range c.store {
		keys = append(keys, key)
	}
	c.mutex.RUnlock()
	return keys, nil
}

func (c *InMemoryStorage) Clear() error {
	c.mutex.Lock()
	c.store = make(map[string]map[string]*InMemoryStorageEntry)
	c.mutex.Unlock()
	return nil
}

func (c *InMemoryStorage) Cleanup(key string, config *KeyConfig, expireBeforeTime time.Time) error {
	c.mutex.Lock()
	for params, value := range c.store[key] {
		if expireBeforeTime.After(value.Timestamp) {
			// its safe to delete from maps during a range loop
			delete(c.store[key], params)
		}
	}
	if len(c.store[key]) == 0 {
		delete(c.store, key)
	}
	c.mutex.Unlock()
	return nil
}

func (c *InMemoryStorage) EntryCount() (int64, error) {
	var count int64
	c.mutex.RLock()
	for _, paramsMap := range c.store {
		count += int64(len(paramsMap))
	}
	c.mutex.RUnlock()
	return count, nil
}

func (c *InMemoryStorage) ExpiredEntryCount(key string, config *KeyConfig, expireBeforeTime time.Time) (int64, error) {
	var count int64 = 0
	c.mutex.RLock()
	for _, value := range c.store[key] {
		if expireBeforeTime.After(value.Timestamp) {
			count += 1
		}
	}
	c.mutex.RUnlock()
	return count, nil
}

func (c *InMemoryStorage) Dump(n int64) {
	// snapshot the entries while holding the lock, then print without it
	type row struct {
		key    string
		params string
		value  *InMemoryStorageEntry
	}
	var rows []row
	c.mutex.RLock()
	for key, paramsMap := range c.store {
		for params, value := range paramsMap {
			rows = append(rows, row{key, params, value})
		}
	}
	c.mutex.RUnlock()

	var count int64
	for _, r := range rows {
		if count >= n {
			fmt.Println("...")
			break
		}
		count += 1
		fmt.Printf("fullkey=%s:%s timestamp=%s compression_type=%s data=\n", r.key, r.params, r.value.Timestamp, r.value.CompressionType)
		fmt.Println(hex.Dump(r.value.Data))
	}
	if count == 0 {
		fmt.Println("No entries")
	}
}
