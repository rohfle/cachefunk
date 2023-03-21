package cachefunk

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type GORMCache struct {
	DB                *gorm.DB
	IgnoreCacheCtxKey CtxKey
}

type CacheEntry struct {
	ID           int64      `json:"id" gorm:"primaryKey"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    *time.Time `json:"expires_at"`
	Key          string     `json:"key" gorm:"uniqueIndex:idx_key_params;not null"`
	Params       string     `json:"params" gorm:"uniqueIndex:idx_key_params;not null"`
	IsCompressed bool       `json:"is_compressed" gorm:"default:false;not null"`
	Data         []byte     `json:"data" gorm:"not null"`
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

func (c *GORMCache) Get(config *Config, params string) ([]byte, bool) {
	var cacheEntry CacheEntry

	result := c.DB.Where("key = ? AND params = ?", config.Key, params).First(&cacheEntry)
	if result.Error != nil {
		return nil, false
	}
	// if entry has expired, delete and return not found
	timeNow := time.Now().UTC()
	if cacheEntry.ExpiresAt != nil && timeNow.After(*cacheEntry.ExpiresAt) {
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
func (c *GORMCache) Set(config *Config, params string, value []byte) {
	if config.TTL == 0 {
		return // immediately discard the entry
	}
	expiresAt := calculateExpiryTime(config)

	if config.UseCompression {
		var err error
		value, err = compressBytes(value)
		if err != nil {
			return
		}
	}

	c.SetRaw(config.Key, params, value, expiresAt, config.UseCompression)
}

// SetRaw will set a cache value by its key and params
func (c *GORMCache) SetRaw(key string, params string, value []byte, expiresAt *time.Time, useCompression bool) {
	cacheEntry := CacheEntry{
		Key:          key,
		Params:       params,
		Data:         value,
		ExpiresAt:    expiresAt,
		IsCompressed: useCompression,
	}

	// create or update cacheEntry
	c.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}, {Name: "params"}},
		DoUpdates: clause.AssignmentColumns([]string{"data", "expires_at", "is_compressed"}),
	}).Create(&cacheEntry)
}

// Clear will delete all cache entries
func (c *GORMCache) Clear() {
	c.DB.Where("1 = 1").Delete(&CacheEntry{})
}

// Cleanup will delete all cache entries that have expired
func (c *GORMCache) Cleanup(cutoff *time.Time) {
	if cutoff == nil {
		t := time.Now().UTC()
		cutoff = &t
	}
	c.DB.Where("expires_at IS NOT NULL AND expires_at < ?", cutoff).Delete(&CacheEntry{})
}

func (c *GORMCache) EntryCount() int64 {
	var count int64
	c.DB.Model(&CacheEntry{}).Count(&count)
	return count
}

func (c *GORMCache) ExpiredEntryCount(cutoff *time.Time) int64 {
	if cutoff == nil {
		t := time.Now().UTC()
		cutoff = &t
	}

	var count int64
	c.DB.Model(&CacheEntry{}).Where("expires_at IS NOT NULL AND expires_at < ?",
		cutoff,
	).Count(&count)
	return count
}
