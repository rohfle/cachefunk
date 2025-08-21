package cachefunk

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DiskStorage stores cached items on disk in a tree of folders
type DiskStorage struct {
	BasePath      string
	CalculatePath DiskStoragePather
}

// checkDirWritable checks if the directory at the given path is writable.
// It returns an error if the directory is not writable, nil otherwise.
func checkDirWritable(dirPath string) error {
	// os.CreateTemp creates a temporary file in the specified directory.
	// This is the most reliable way to test writability.
	tmpfile, err := os.CreateTemp(dirPath, "writable-test-")
	if err != nil {
		// If we can't create the file, the directory isn't writable.
		return fmt.Errorf("%q is not writable: %w", dirPath, err)
	}
	// Close and remove the temporary file to clean up.
	tmpfile.Close()
	os.Remove(tmpfile.Name())
	return nil
}

func NewDiskStorage(basePath string, pather DiskStoragePather) (*DiskStorage, error) {
	if pather == nil {
		pather = DefaultDiskStoragePather
	}

	basePath = filepath.Clean(basePath)
	if basePath == "" {
		return nil, errors.New("base path of DiskStorage should not be empty")
	}

	// Make sure the base path directory exists
	err := os.MkdirAll(basePath, 0755)
	if err != nil {
		return nil, fmt.Errorf("when creating base path of DiskStorage %q: %w", basePath, err)
	}

	// Check basePath is writable
	if err = checkDirWritable(basePath); err != nil {
		return nil, fmt.Errorf("base path of DiskStorage %w", err)
	}

	cache := DiskStorage{
		BasePath:      basePath,
		CalculatePath: pather,
	}
	return &cache, nil
}

func (c *DiskStorage) getCacheItemPath(cacheKey string, config *KeyConfig, params string) (string, error) {
	if cacheKey == "" || cacheKey[0] == '.' {
		return "", fmt.Errorf("cache key cannot be blank or begin with a dot")
	}
	parts := c.CalculatePath(cacheKey, params)
	relPath := filepath.Join(parts...)
	if strings.HasPrefix(relPath, "..") || strings.HasPrefix(relPath, "/") {
		return "", fmt.Errorf("generated path %q must be inside cache root", relPath)
	}
	path := filepath.Join(c.BasePath, relPath)
	if compression := config.GetBodyCompression(); compression != NoCompression {
		path += "." + compression.String()
	}
	return path, nil
}

func (c *DiskStorage) Get(key string, config *KeyConfig, params string, expireTime time.Time) ([]byte, error) {
	path, err := c.getCacheItemPath(key, config, params)
	if err != nil {
		return nil, fmt.Errorf("cache.Get: %w", err)
	}
	// Check if path exists
	stat, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrEntryNotFound
		}
		return nil, fmt.Errorf("call to os.Stat failed %q: %v", path, err)
	}

	if expireTime.After(stat.ModTime()) {
		// Item has expired but DO NOT REMOVE THE ITEM.
		// If FallbackToExpired is set, expired value will be used if retrieve function fails.
		return nil, ErrEntryExpired
	}

	value, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrEntryNotFound
		}
		return nil, fmt.Errorf("failed to read %q: %v", path, err)
	}
	return value, nil
}

func (c *DiskStorage) Set(key string, config *KeyConfig, params string, value []byte, timestamp time.Time) error {
	path, err := c.getCacheItemPath(key, config, params)
	if err != nil {
		return fmt.Errorf("cache.Set: %w", err)
	}
	dirs, _ := filepath.Split(path)
	err = os.MkdirAll(dirs, 0755)
	if err != nil {
		return fmt.Errorf("call to os.MkdirAll failed %+v: %v", dirs, err)
	}
	err = os.WriteFile(path, value, 0644)
	if err != nil {
		return fmt.Errorf("call to os.WriteFile failed %q: %v", path, err)
	}
	err = os.Chtimes(path, time.Now().UTC(), timestamp)
	if err != nil {
		return fmt.Errorf("call to os.Chtimes failed %q: %v", path, err)
	}
	return nil
}

// Clear will delete all cache entries
func (c *DiskStorage) Clear() error {
	err := os.RemoveAll(c.BasePath)
	if err != nil {
		return fmt.Errorf("failed to clear cache %q: when removing files: %v", c.BasePath, err)
	}
	err = os.Mkdir(c.BasePath, 0755)
	if err != nil {
		return fmt.Errorf("failed to clear cache %q: when recreating directory: %v", c.BasePath, err)
	}
	return nil
}

// Cleanup will delete all cache entries that have expired
func (c *DiskStorage) Cleanup(key string, config *KeyConfig, expireTime time.Time) error {
	basePath := filepath.Join(c.BasePath, key)
	if _, err := os.Stat(basePath); os.IsNotExist(err) {
		return nil // key doesnt exist therefore nothing to do
	}
	c.IterateFiles(basePath, func(parent string, file fs.DirEntry) {
		path := filepath.Join(parent, file.Name())
		info, err := file.Info()
		if err != nil {
			warning("skipping %q: failed to get info: %v", path, err)
			return
		}

		if expireTime.After(info.ModTime()) {
			err := os.Remove(path)
			if err != nil {
				warning("skipping %q: file cleanup failed: %v", path, err)
			}
		}
	})
	return nil
}

func (c *DiskStorage) EntryCount() (int64, error) {
	var count int64
	c.IterateFiles(c.BasePath, func(parent string, file fs.DirEntry) {
		count += 1
	})
	return count, nil
}

func (c *DiskStorage) ExpiredEntryCount(key string, config *KeyConfig, expireTime time.Time) (int64, error) {
	var count int64
	basePath := filepath.Join(c.BasePath, key)
	if _, err := os.Stat(basePath); os.IsNotExist(err) {
		return 0, nil // key doesnt exist therefore count is 0
	}
	c.IterateFiles(basePath, func(parent string, file fs.DirEntry) {
		path := filepath.Join(parent, file.Name())
		info, err := file.Info()
		if err != nil {
			warning("skipping %q: failed to get info: %v", path, err)
			return
		}

		if expireTime.After(info.ModTime()) {
			count += 1
		}
	})
	return count, nil
}

func (c *DiskStorage) IterateFiles(basePath string, callback func(string, fs.DirEntry)) {
	dirsLeft := []string{basePath}
	var curDir string
	for len(dirsLeft) > 0 {
		curDir, dirsLeft = dirsLeft[0], dirsLeft[1:]
		entries, err := os.ReadDir(curDir)
		if err != nil {
			warning("skipping %q: failed to read directory: %v", curDir, err)
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

// Dump prints out a sample hexdump of cached content
func (c *DiskStorage) Dump(n int64) {
	var count int64
	basePath := c.BasePath
	if _, err := os.Stat(basePath); !os.IsNotExist(err) {
		c.IterateFiles(basePath, func(parent string, file fs.DirEntry) {
			if count == n {
				fmt.Println("...")
			}
			count += 1
			if count > n {
				return
			}
			path := filepath.Join(parent, file.Name())
			info, err := file.Info()
			if err != nil {
				warning("skipping %q: failed to get info: %v", path, err)
				return
			}
			fmt.Printf("path=%s timestamp=%s data=\n", path, info.ModTime())
			data, err := os.ReadFile(path)
			if err != nil {
				fmt.Println("Error:", err)
			}
			fmt.Println(hex.Dump(data))
		})
	}
	if count == 0 {
		fmt.Println("No entries")
	}
}
