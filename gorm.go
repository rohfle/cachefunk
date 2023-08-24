package cachefunk

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type GORMCache struct {
	CacheConfig       *CacheFunkConfig
	DB                *gorm.DB
	IgnoreCacheCtxKey CtxKey
}

func (c *GORMCache) SetConfig(config *CacheFunkConfig) {
	c.CacheConfig = config
}

type CacheEntry struct {
	ID           int64     `json:"id" gorm:"primaryKey"`
	Timestamp    time.Time `json:"timestamp" gorm:"not null"`
	Key          string    `json:"key" gorm:"uniqueIndex:idx_key_params;not null"`
	Params       string    `json:"params" gorm:"uniqueIndex:idx_key_params;not null"`
	IsCompressed bool      `json:"is_compressed" gorm:"default:false;not null"`
	Data         []byte    `json:"data" gorm:"not null"`
}

func NewGORMCache(db *gorm.DB) *GORMCache {
	cache := GORMCache{
		DB: db.Session(&gorm.Session{
			Logger: logger.Default.LogMode(logger.Silent),
		}),
		IgnoreCacheCtxKey: DEFAULT_IGNORE_CACHE_CTX_KEY,
	}
	db.AutoMigrate(&CacheEntry{})
	return &cache
}

func (c *GORMCache) GetIgnoreCacheCtxKey() CtxKey {
	return c.IgnoreCacheCtxKey
}

func (c *GORMCache) Get(key string, params string) ([]byte, bool) {
	var cacheEntry CacheEntry

	result := c.DB.Where("key = ? AND params = ?", key, params).First(&cacheEntry)
	if result.Error != nil {
		return nil, false
	}
	// if entry has expired, delete and return not found
	config := c.CacheConfig.Get(key)
	expiry := cacheEntry.Timestamp.Add(time.Second * time.Duration(config.TTL))
	if time.Now().UTC().After(expiry) {
		c.DB.Delete(&cacheEntry)
		return nil, false
	}

	value := cacheEntry.Data
	if cacheEntry.IsCompressed {
		var err error
		value, err = decompressBytes(value)
		if err != nil {
			return nil, false
		}
	}
	return value, true
}

// Set will set a cache value by its key and params
func (c *GORMCache) Set(key string, params string, value []byte) {
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

// SetRaw will set a cache value by its key and params
func (c *GORMCache) SetRaw(key string, params string, value []byte, timestamp time.Time, useCompression bool) {
	cacheEntry := CacheEntry{
		Key:          key,
		Params:       params,
		Data:         value,
		Timestamp:    timestamp,
		IsCompressed: useCompression,
	}

	// create or update cacheEntry
	c.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}, {Name: "params"}},
		DoUpdates: clause.AssignmentColumns([]string{"data", "timestamp", "is_compressed"}),
	}).Create(&cacheEntry)
}

// Clear will delete all cache entries
func (c *GORMCache) Clear() {
	c.DB.Where("1 = 1").Delete(&CacheEntry{})
}

// Cleanup will delete all cache entries that have expired
func (c *GORMCache) Cleanup() {
	now := time.Now().UTC()
	for key, config := range c.CacheConfig.Configs {
		cutoff := now.Add(-1 * time.Duration(config.TTL) * time.Second)
		c.DB.Where("key = ? AND timestamp < ?", key, cutoff).Delete(&CacheEntry{})
	}
}

func (c *GORMCache) EntryCount() int64 {
	var count int64
	c.DB.Model(&CacheEntry{}).Count(&count)
	return count
}

func (c *GORMCache) ExpiredEntryCount() int64 {
	now := time.Now().UTC()
	var total int64
	for key, config := range c.CacheConfig.Configs {
		cutoff := now.Add(-1 * time.Duration(config.TTL) * time.Second)
		var count int64
		c.DB.Model(&CacheEntry{}).Where("key = ? AND timestamp < ?", key, cutoff).Count(&count)
		total += count
	}
	return total
}
