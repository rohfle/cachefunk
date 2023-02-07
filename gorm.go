package cachefunk

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type GORMCache struct {
	DB *gorm.DB
}

type CacheEntry struct {
	ID        int64      `json:"id" gorm:"primaryKey"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at"`
	Key       string     `json:"key" gorm:"uniqueIndex:idx_key_params;not null"`
	Params    string     `json:"params" gorm:"uniqueIndex:idx_key_params;not null"`
	Data      []byte     `json:"data" gorm:"not null"`
}

func NewGORMCache(db *gorm.DB) *GORMCache {
	cache := GORMCache{
		DB: db.Session(&gorm.Session{
			Logger: logger.Default.LogMode(logger.Silent),
		}),
	}
	db.AutoMigrate(&CacheEntry{})
	return &cache
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
	return cacheEntry.Data, true
}

// Set will set a cache value by its key and params
func (c *GORMCache) Set(config *Config, params string, value []byte) {
	if config.TTL == 0 {
		return // immediately discard the entry
	}
	expiresAt := calculateExpiryTime(config)

	cacheEntry := CacheEntry{
		Key:       config.Key,
		Params:    params,
		Data:      value,
		ExpiresAt: expiresAt,
	}

	// create or update cacheEntry
	c.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}, {Name: "params"}},
		DoUpdates: clause.AssignmentColumns([]string{"data", "expires_at"}),
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
