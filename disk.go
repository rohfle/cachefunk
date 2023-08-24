package cachefunk

import (
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type DiskCache struct {
	CacheConfig       *CacheFunkConfig
	BasePath          string
	CalculatePath     func(cacheKey string, params string) []string
	IgnoreCacheCtxKey CtxKey
}

func (c *DiskCache) SetConfig(config *CacheFunkConfig) {
	c.CacheConfig = config
}

// Returns the
func DefaultCalculatePath(cacheKey string, params string) []string {
	data := sha256.Sum256([]byte(params))
	hash := base64.URLEncoding.EncodeToString(data[:])
	return []string{cacheKey, hash[0:2], hash[2:4], hash}
}

func NewDiskCache(basePath string, calcPathFn ...func(string, string) []string) *DiskCache {
	if len(calcPathFn) == 0 {
		calcPathFn = append(calcPathFn, DefaultCalculatePath)
	}

	cache := DiskCache{
		BasePath:          basePath,
		CalculatePath:     calcPathFn[0],
		IgnoreCacheCtxKey: DEFAULT_IGNORE_CACHE_CTX_KEY,
	}
	return &cache
}

func (c *DiskCache) GetIgnoreCacheCtxKey() CtxKey {
	return c.IgnoreCacheCtxKey
}

func (c *DiskCache) getCacheItemPath(cacheKey string, params string, useCompression bool) string {
	bits := append([]string{c.BasePath}, c.CalculatePath(cacheKey, params)...)
	path := filepath.Join(bits...)
	if useCompression {
		path += ".gz"
	}
	return path
}

func (c *DiskCache) Get(key string, params string) ([]byte, bool) {
	config := c.CacheConfig.Get(key)
	path := c.getCacheItemPath(key, params, config.UseCompression)

	// check if path exists
	stat, err := os.Stat(path)
	if err != nil {
		return nil, false
	}

	// check if path modtime is older than ttl
	expiry := stat.ModTime().Add(time.Second * time.Duration(config.TTL))
	if time.Now().UTC().After(expiry) {
		os.Remove(path)
		return nil, false
	}

	value, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}

	// if data is compressed, decompress before return
	if config.UseCompression {
		var err error
		value, err = decompressBytes(value)
		if err != nil {
			return nil, false
		}
	}
	return value, true
}

// Set will set a cache value by its key and params
func (c *DiskCache) Set(key string, params string, value []byte) {
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

func (c *DiskCache) SetRaw(key string, params string, value []byte, timestamp time.Time, useCompression bool) {
	path := c.getCacheItemPath(key, params, useCompression)
	dirs, _ := filepath.Split(path)
	os.MkdirAll(dirs, 0755)
	os.WriteFile(path, value, 0644)
	os.Chtimes(path, time.Now().UTC(), timestamp)
}

// Clear will delete all cache entries
func (c *DiskCache) Clear() {
	os.RemoveAll(c.BasePath)
	os.Mkdir(c.BasePath, 0755)
}

// Cleanup will delete all cache entries that have expired
func (c *DiskCache) Cleanup() {
	now := time.Now().UTC()
	for key, config := range c.CacheConfig.Configs {
		basePath := filepath.Join(c.BasePath, key)
		cutoff := now.Add(-1 * time.Duration(config.TTL) * time.Second)
		c.IterateFiles(basePath, func(parent string, file fs.DirEntry) {
			if info, err := file.Info(); err == nil {
				if info.ModTime().Before(cutoff) {
					os.Remove(filepath.Join(parent, file.Name()))
				}
			}
		})
	}
}

func (c *DiskCache) EntryCount() int64 {
	var count int64
	c.IterateFiles(c.BasePath, func(parent string, file fs.DirEntry) {
		count += 1
	})
	return count
}

func (c *DiskCache) ExpiredEntryCount() int64 {
	var count int64
	now := time.Now().UTC()
	for key, config := range c.CacheConfig.Configs {
		basePath := filepath.Join(c.BasePath, key)
		cutoff := now.Add(-1 * time.Duration(config.TTL) * time.Second)
		c.IterateFiles(basePath, func(parent string, file fs.DirEntry) {
			if info, err := file.Info(); err == nil {
				if info.ModTime().Before(cutoff) {
					count += 1
				}
			}
		})
	}
	return count
}

func (c *DiskCache) IterateFiles(basePath string, callback func(string, fs.DirEntry)) {
	dirsLeft := []string{basePath}
	var curDir string
	for len(dirsLeft) > 0 {
		curDir, dirsLeft = dirsLeft[0], dirsLeft[1:]
		entries, err := os.ReadDir(curDir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() {
				dirsLeft = append(dirsLeft, filepath.Join(curDir, entry.Name()))
			} else {
				callback(curDir, entry)
			}
		}
	}
}
