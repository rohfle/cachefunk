package cachefunk

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type InMemoryStorageEntry struct {
	Data            []byte
	Timestamp       time.Time
	CompressionType string
}

// InMemoryStorage stores cache entries in a map
type InMemoryStorage struct {
	Store map[string]*InMemoryStorageEntry
}

func NewInMemoryStorage() *InMemoryStorage {
	cache := InMemoryStorage{
		Store: make(map[string]*InMemoryStorageEntry, 0),
	}
	return &cache
}

func (c *InMemoryStorage) Get(key string, config *KeyConfig, params string, expireTime time.Time) ([]byte, error) {
	fullKey := key + ":" + params
	value, found := c.Store[fullKey]
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

	c.Store[fullKey] = &InMemoryStorageEntry{
		Data:            value,
		Timestamp:       timestamp,
		CompressionType: config.GetBodyCompression().String(),
	}
	return nil
}

func (c *InMemoryStorage) Clear() error {
	c.Store = make(map[string]*InMemoryStorageEntry, 0)
	return nil
}

func (c *InMemoryStorage) Cleanup(key string, config *KeyConfig, expireTime time.Time) error {
	var expiredKeys []string
	for fullkey, value := range c.Store {
		if !strings.HasPrefix(fullkey, key+":") {
			continue
		}
		if expireTime.After(value.Timestamp) {
			expiredKeys = append(expiredKeys, fullkey)
		}
	}
	for _, fullkey := range expiredKeys {
		delete(c.Store, fullkey)
	}
	return nil
}

func (c *InMemoryStorage) EntryCount() (int64, error) {
	return int64(len(c.Store)), nil
}

func (c *InMemoryStorage) ExpiredEntryCount(key string, config *KeyConfig, expireTime time.Time) (int64, error) {
	var count int64 = 0
	for fullkey, value := range c.Store {
		if !strings.HasPrefix(fullkey, key+":") {
			continue
		}
		if expireTime.After(value.Timestamp) {
			count += 1
		}
	}
	return count, nil
}

func (c *InMemoryStorage) Dump(n int64) {
	var count int64
	for fullkey, value := range c.Store {
		if count >= n {
			fmt.Println("...")
			break
		}
		count += 1
		fmt.Printf("fullkey=%s timestamp=%s compression_type=%s data=\n", fullkey, value.Timestamp, value.CompressionType)
		fmt.Println(hex.Dump(value.Data))
	}
	if count == 0 {
		fmt.Println("No entries")
	}
}
