package cachefunk

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// GORMStorage stores cached items in a database table.
//
// A GORMStorage is safe for concurrent use by multiple goroutines. The zero value
// is not usable: call [NewGORMStorage].
type GORMStorage struct {
	DB *gorm.DB
}

// CacheEntry is the cache table.
// (key, params_hash) is unique and serves Get and the upsert in Set
// (key, timestamp) serves Cleanup and ExpiredEntryCount
//
// ParamsHash holds a sha256 of Params used for indexing so a long params can't exceed
// index entry limits and fail to store.
// Params is kept for debugging and to confirm a match.
type CacheEntry struct {
	ID              int64     `json:"id" gorm:"primaryKey"`
	Timestamp       time.Time `json:"timestamp" gorm:"not null;index:idx_key_timestamp,priority:2"`
	Key             string    `json:"key" gorm:"uniqueIndex:idx_key_params_hash,priority:1;index:idx_key_timestamp,priority:1;not null"`
	Params          string    `json:"params" gorm:"not null"`
	ParamsHash      []byte    `json:"params_hash" gorm:"uniqueIndex:idx_key_params_hash,priority:2;not null"`
	CompressionType string    `json:"compression_type" gorm:"not null"`
	Data            []byte    `json:"data" gorm:"not null"`
}

// paramsHash is what the indexed params_hash column holds: a sha256 of the
// serialised params, always 32 bytes, so no params value can exceed a btree key
// limit.
func paramsHash(params string) []byte {
	sum := sha256.Sum256([]byte(params))
	return sum[:]
}

// NewGORMStorage returns a storage using db, migrating the cache table into
// existence if it is absent. Migration never drops a column or index, so a layout
// change that removes or renames one needs the old table dropped first.
func NewGORMStorage(db *gorm.DB) (*GORMStorage, error) {
	cache := GORMStorage{
		DB: db.Session(&gorm.Session{
			Logger: logger.Default.LogMode(logger.Silent),
			// every statement here is a single upsert, delete or count, so the
			// transaction gorm wraps around each write only adds round trips
			SkipDefaultTransaction: true,
		}),
	}
	err := db.AutoMigrate(&CacheEntry{})
	if err != nil {
		return nil, fmt.Errorf("error while migrating: %w", err)
	}
	return &cache, nil
}

func (c *GORMStorage) Get(key string, config *KeyConfig, params string, expireBeforeTime time.Time) ([]byte, error) {
	var cacheEntry CacheEntry

	// map conditions and clause.Eq make gorm quote the column names: a raw
	// "key = ?" fragment is passed through unquoted, and key is a reserved word
	// in MySQL. The hash goes through clause.Eq because a []byte inside a map
	// condition is expanded into an IN list of byte values, which never matches.
	// params is compared as well, so a hash collision cannot return another
	// entry's value
	result := c.DB.
		Where(map[string]any{"key": key, "params": params}).
		Where(clause.Eq{Column: clause.Column{Name: "params_hash"}, Value: paramsHash(params)}).
		First(&cacheEntry)
	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, ErrEntryNotFound
		}
		return nil, result.Error
	}

	if cacheEntry.CompressionType != config.GetBodyCompression().String() {
		// yes there is an entry but it has the wrong compression type
		// so it is the same as not found
		return nil, ErrEntryNotFound
	}

	value := cacheEntry.Data

	// if entry has expired, delete and return not found
	if expireBeforeTime.After(cacheEntry.Timestamp) {
		// item has expired but DO NOT REMOVE THE ITEM
		// if FallbackToExpired option set expired value
		// will be used if the resolver fails
		return value, ErrEntryExpired
	}
	return value, nil
}

// Set replaces any existing entry for the same key and params.
func (c *GORMStorage) Set(key string, config *KeyConfig, params string, value []byte, timestamp time.Time) error {
	cacheEntry := CacheEntry{
		Key:             key,
		Params:          params,
		ParamsHash:      paramsHash(params),
		Data:            value,
		Timestamp:       timestamp,
		CompressionType: config.GetBodyCompression().String(),
	}

	result := c.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}, {Name: "params_hash"}},
		DoUpdates: clause.AssignmentColumns([]string{"params", "data", "timestamp", "compression_type"}),
	}).Create(&cacheEntry)
	if result.Error != nil {
		return result.Error
	}

	return nil
}

func (c *GORMStorage) Keys() ([]string, error) {
	var keys []string
	// Distinct and Pluck build quoted column references, unlike a raw SQL
	// string, so this works on dialects where key is a reserved word
	result := c.DB.Model(&CacheEntry{}).Distinct().Pluck("key", &keys)
	if result.Error != nil {
		return nil, result.Error
	}
	return keys, nil
}

// Clear deletes every row in the cache table.
func (c *GORMStorage) Clear() error {
	// gorm refuses a delete that has no conditions, and "1 = 1" is the condition
	result := c.DB.Where("1 = 1").Delete(&CacheEntry{})
	if result.Error != nil {
		return result.Error
	}
	return nil
}

// Cleanup deletes the expired entries stored under one key.
func (c *GORMStorage) Cleanup(key string, config *KeyConfig, expireBeforeTime time.Time) error {
	result := c.DB.
		Where(map[string]any{"key": key}).
		Where(clause.Lt{Column: clause.Column{Name: "timestamp"}, Value: expireBeforeTime}).
		Delete(&CacheEntry{})
	if result.Error != nil {
		return result.Error
	}
	return nil
}

// EntryCount returns the number of rows in the cache table.
func (c *GORMStorage) EntryCount() (int64, error) {
	var count int64
	result := c.DB.Model(&CacheEntry{}).Count(&count)
	if result.Error != nil {
		return 0, result.Error
	}
	return count, nil
}

func (c *GORMStorage) ExpiredEntryCount(key string, config *KeyConfig, expireBeforeTime time.Time) (int64, error) {
	var count int64
	result := c.DB.Model(&CacheEntry{}).
		Where(map[string]any{"key": key}).
		Where(clause.Lt{Column: clause.Column{Name: "timestamp"}, Value: expireBeforeTime}).
		Count(&count)
	if result.Error != nil {
		return 0, result.Error
	}
	return count, nil
}

func (c *GORMStorage) dump(n int64) {
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
