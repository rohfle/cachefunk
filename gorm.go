package cachefunk

import (
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type GORMStorage struct {
	DB *gorm.DB
}

type CacheEntry struct {
	ID              int64     `json:"id" gorm:"primaryKey"`
	Timestamp       time.Time `json:"timestamp" gorm:"not null"`
	Key             string    `json:"key" gorm:"uniqueIndex:idx_key_params;not null"`
	Params          string    `json:"params" gorm:"uniqueIndex:idx_key_params;not null"`
	CompressionType string    `json:"compression_type" gorm:"not null"`
	Data            []byte    `json:"data" gorm:"not null"`
}

func NewGORMStorage(db *gorm.DB) (*GORMStorage, error) {
	cache := GORMStorage{
		DB: db.Session(&gorm.Session{
			Logger: logger.Default.LogMode(logger.Silent),
		}),
	}
	err := db.AutoMigrate(&CacheEntry{})
	if err != nil {
		return nil, fmt.Errorf("error while migrating: %w", err)
	}
	return &cache, nil
}

func (c *GORMStorage) Get(key string, config *KeyConfig, params string, expireTime time.Time) ([]byte, error) {
	var cacheEntry CacheEntry

	result := c.DB.Where("key = ? AND params = ?", key, params).First(&cacheEntry)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrEntryNotFound
		}
		// other errors return as is
		return nil, result.Error
	}

	if cacheEntry.CompressionType != config.GetBodyCompression().String() {
		// yes there is an entry but it has the wrong compression type
		// so it is the same as not found
		return nil, ErrEntryNotFound
	}

	// if entry has expired, delete and return not found
	if expireTime.After(cacheEntry.Timestamp) {
		// item has expired but DO NOT REMOVE THE ITEM
		// if FallbackToExpired option set expired value
		// will be used if retrieve function fails
		return nil, ErrEntryExpired
	}

	value := cacheEntry.Data
	return value, nil
}

// SetRaw will set a cache value by its key and params
func (c *GORMStorage) Set(key string, config *KeyConfig, params string, value []byte, timestamp time.Time) error {
	cacheEntry := CacheEntry{
		Key:             key,
		Params:          params,
		Data:            value,
		Timestamp:       timestamp,
		CompressionType: config.GetBodyCompression().String(),
	}

	// create or update cacheEntry
	result := c.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}, {Name: "params"}},
		DoUpdates: clause.AssignmentColumns([]string{"data", "timestamp", "compression_type"}),
	}).Create(&cacheEntry)
	if result.Error != nil {
		return result.Error
	}

	return nil
}

// Clear will delete all cache entries
func (c *GORMStorage) Clear() error {
	result := c.DB.Where("1 = 1").Delete(&CacheEntry{})
	if result.Error != nil {
		return result.Error
	}
	return nil
}

// Cleanup will delete all cache entries that have expired
func (c *GORMStorage) Cleanup(key string, config *KeyConfig, expireTime time.Time) error {
	result := c.DB.Where("key = ? AND timestamp < ?", key, expireTime).Delete(&CacheEntry{})
	if result.Error != nil {
		return result.Error
	}
	return nil
}

func (c *GORMStorage) EntryCount() (int64, error) {
	var count int64
	result := c.DB.Model(&CacheEntry{}).Count(&count)
	if result.Error != nil {
		return 0, result.Error
	}
	return count, nil
}

func (c *GORMStorage) ExpiredEntryCount(key string, config *KeyConfig, expireTime time.Time) (int64, error) {
	var count int64
	result := c.DB.Model(&CacheEntry{}).Where("key = ? AND timestamp < ?", key, expireTime).Count(&count)
	if result.Error != nil {
		return 0, result.Error
	}
	return count, nil
}

func (c *GORMStorage) Dump(n int64) {
	var results []*CacheEntry
	result := c.DB.Model(&CacheEntry{}).Find(&results)
	if result.Error != nil {
		fmt.Println("Error:", result.Error)
		return
	}

	var count int64
	for _, entry := range results {
		if count >= n {
			fmt.Println("...")
			break
		}
		count += 1
		fmt.Printf("key=%s params=%s timestamp=%s compression_type=%s data=\n", entry.Key, entry.Params, entry.Timestamp, entry.CompressionType)
		fmt.Println(hex.Dump(entry.Data))
	}

	if count == 0 {
		fmt.Println("No entries")
	}
}
