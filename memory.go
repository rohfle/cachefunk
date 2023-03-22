package cachefunk

import (
	"time"
)

type InMemoryCacheEntry struct {
	Data         string
	ExpiresAt    *time.Time
	IsCompressed bool
}

type InMemoryCache struct {
	Store             map[string]*InMemoryCacheEntry
	IgnoreCacheCtxKey CtxKey
}

func NewInMemoryCache() *InMemoryCache {
	cache := InMemoryCache{
		Store:             make(map[string]*InMemoryCacheEntry, 0),
		IgnoreCacheCtxKey: DEFAULT_IGNORE_CACHE_CTX_KEY,
	}
	return &cache
}

func (c *InMemoryCache) GetIgnoreCacheCtxKey() CtxKey {
	return c.IgnoreCacheCtxKey
}

func (c *InMemoryCache) Get(config Config, params string) ([]byte, bool) {
	fullKey := config.Key + ": " + params
	value, found := c.Store[fullKey]
	if !found {
		return nil, false
	}
	// check if cached value has expired
	timeNow := time.Now().UTC()
	if value.ExpiresAt != nil && timeNow.After(*value.ExpiresAt) {
		delete(c.Store, fullKey)
		return nil, false
	}

	data := []byte(value.Data)

	if value.IsCompressed {
		var err error
		data, err = decompressBytes(data)
		if err != nil {
			return nil, false
		}
	}

	return data, true
}

func (c *InMemoryCache) Set(config Config, params string, value []byte) {
	if config.TTL == 0 {
		return // immediately discard the entry
	}
	expiresAt := calculateExpiryTime(&config)

	if config.UseCompression {
		var err error
		value, err = compressBytes(value)
		if err != nil {
			return
		}
	}

	c.SetRaw(config.Key, params, value, expiresAt, config.UseCompression)
}

func (c *InMemoryCache) SetRaw(key string, params string, value []byte, expiresAt *time.Time, isCompressed bool) {
	fullKey := key + ": " + params
	c.Store[fullKey] = &InMemoryCacheEntry{
		Data:         string(value),
		ExpiresAt:    expiresAt,
		IsCompressed: isCompressed,
	}
}

func (c *InMemoryCache) Clear() {
	c.Store = make(map[string]*InMemoryCacheEntry, 0)
}

func (c *InMemoryCache) Cleanup(cutoff *time.Time) {
	if cutoff == nil {
		t := time.Now().UTC()
		cutoff = &t
	}
	var expiredKeys []string
	for key, value := range c.Store {
		if value.ExpiresAt != nil && value.ExpiresAt.Before(*cutoff) {
			expiredKeys = append(expiredKeys, key)
		}
	}

	for _, key := range expiredKeys {
		delete(c.Store, key)
	}
}

func (c *InMemoryCache) EntryCount() int64 {
	return int64(len(c.Store))
}

func (c *InMemoryCache) ExpiredEntryCount(cutoff *time.Time) int64 {
	var count int64 = 0
	if cutoff == nil {
		t := time.Now().UTC()
		cutoff = &t
	}
	for _, value := range c.Store {
		if value.ExpiresAt != nil && value.ExpiresAt.Before(*cutoff) {
			count += 1
		}
	}
	return count
}
