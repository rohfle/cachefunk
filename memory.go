package cachefunk

import (
	"strings"
	"time"
)

type InMemoryCacheEntry struct {
	Data         string
	Timestamp    time.Time
	IsCompressed bool
}

type InMemoryCache struct {
	CacheConfig       *CacheFunkConfig
	Store             map[string]*InMemoryCacheEntry
	IgnoreCacheCtxKey CtxKey
}

func (c *InMemoryCache) SetConfig(config *CacheFunkConfig) {
	c.CacheConfig = config
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

func (c *InMemoryCache) Get(key string, params string) ([]byte, bool) {
	fullKey := key + ":" + params
	value, found := c.Store[fullKey]
	if !found {
		return nil, false
	}
	// check if cached value has expired
	config := c.CacheConfig.Get(key)
	expiry := value.Timestamp.Add(time.Second * time.Duration(config.TTL))
	if time.Now().UTC().After(expiry) {
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

func (c *InMemoryCache) Set(key string, params string, value []byte) {
	config := c.CacheConfig.Get(key)
	if config.TTL <= 0 {
		return // immediately discard the entry
	}

	timestamp := time.Now().UTC()
	if config.TTLJitter > 0 {
		timestamp = timestamp.Add(-1 * time.Duration(config.TTLJitter) * time.Second)
	}

	if config.UseCompression {
		var err error
		value, err = compressBytes(value)
		if err != nil {
			return
		}
	}

	c.SetRaw(key, params, value, timestamp, config.UseCompression)
}

func (c *InMemoryCache) SetRaw(key string, params string, value []byte, timestamp time.Time, isCompressed bool) {
	fullKey := key + ":" + params
	c.Store[fullKey] = &InMemoryCacheEntry{
		Data:         string(value),
		Timestamp:    timestamp,
		IsCompressed: isCompressed,
	}
}

func (c *InMemoryCache) Clear() {
	c.Store = make(map[string]*InMemoryCacheEntry, 0)
}

func (c *InMemoryCache) Cleanup() {
	now := time.Now().UTC()
	for key, config := range c.CacheConfig.Configs {
		cutoff := now.Add(-1 * time.Duration(config.TTL) * time.Second)
		var expiredKeys []string
		for fullkey, value := range c.Store {
			if strings.HasPrefix(fullkey, key+":") && value.Timestamp.Before(cutoff) {
				expiredKeys = append(expiredKeys, fullkey)
			}
		}
		for _, fullkey := range expiredKeys {
			delete(c.Store, fullkey)
		}
	}
}

func (c *InMemoryCache) EntryCount() int64 {
	return int64(len(c.Store))
}

func (c *InMemoryCache) ExpiredEntryCount() int64 {
	var count int64 = 0
	now := time.Now().UTC()
	for key, config := range c.CacheConfig.Configs {
		cutoff := now.Add(-1 * time.Duration(config.TTL) * time.Second)
		for fullkey, value := range c.Store {
			if strings.HasPrefix(fullkey, key+":") && value.Timestamp.Before(cutoff) {
				count += 1
			}
		}
	}
	return count
}
