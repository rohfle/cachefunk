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

func (c *DiskStorage) keyPath(cacheKey string) (string, error) {
	if cacheKey == "" || cacheKey[0] == '.' || strings.Contains(cacheKey, "..") {
		return "", fmt.Errorf("cache key cannot be blank, begin with a dot or have '..' in it")
	}
	return filepath.Join(c.BasePath, cacheKey), nil
}

func (c *DiskStorage) getCacheItemPath(cacheKey string, config *KeyConfig, params string) (string, error) {
	basePath, err := c.keyPath(cacheKey)
	if err != nil {
		return "", err
	}
	parts := c.CalculatePath(params)
	relPath := filepath.Join(parts...)
	fullPath := filepath.Join(basePath, relPath)
	rel, err := filepath.Rel(basePath, fullPath)
	if err != nil || rel == "" || rel[0] == '.' {
		return "", fmt.Errorf("generated path %q must be inside cache key root", relPath)
	}

	if compression := config.GetBodyCompression(); compression != NoCompression {
		fullPath += "." + compression.String()
	}
	return fullPath, nil
}

func (c *DiskStorage) Get(key string, config *KeyConfig, params string, expireBeforeTime time.Time) ([]byte, error) {
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

	hasExpired := expireBeforeTime.After(stat.ModTime())
	if hasExpired && !config.FallbackToExpired {
		// No need to load expired data if not configued to fallback to expired
		return nil, ErrEntryExpired
	}

	value, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrEntryNotFound
		}
		return nil, fmt.Errorf("failed to read %q: %v", path, err)
	}

	if hasExpired {
		// Item has expired but DO NOT REMOVE THE ITEM.
		// If FallbackToExpired is set, expired value will be used if retrieve function fails.
		return value, ErrEntryExpired
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
	tmp, err := os.CreateTemp(dirs, ".cache-*")
	if err != nil {
		return fmt.Errorf("call to os.CreateTemp failed %q: %v", dirs, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = tmp.Write(value); err != nil {
		tmp.Close()
		return fmt.Errorf("call to tmp.Write failed %q: %v", path, err)
	}
	if err = tmp.Chmod(0644); err != nil {
		tmp.Close()
		return fmt.Errorf("call to tmp.Chmod failed %q: %v", path, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("call to tmp.Close failed %q: %v", path, err)
	}
	err = os.Chtimes(tmpName, time.Now().UTC(), timestamp)
	if err != nil {
		return fmt.Errorf("call to os.Chtimes failed %q: %v", path, err)
	}
	err = os.Rename(tmpName, path)
	if err != nil {
		return fmt.Errorf("call to os.Rename failed %q: %v", path, err)
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
func (c *DiskStorage) Cleanup(key string, config *KeyConfig, expireBeforeTime time.Time) error {
	basePath, err := c.keyPath(key)
	if err != nil {
		return fmt.Errorf("Cleanup: %w", err)
	}
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

		if expireBeforeTime.After(info.ModTime()) {
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

func (c *DiskStorage) ExpiredEntryCount(key string, config *KeyConfig, expireBeforeTime time.Time) (int64, error) {
	var count int64
	basePath, err := c.keyPath(key)
	if err != nil {
		return 0, fmt.Errorf("ExpiredEntryCount: %w", err)
	}
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

		if expireBeforeTime.After(info.ModTime()) {
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
