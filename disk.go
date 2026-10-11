package cachefunk

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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
		return nil, fmt.Errorf("call to os.Stat failed %q: %w", path, err)
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
		return nil, fmt.Errorf("failed to read %q: %w", path, err)
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
		return fmt.Errorf("call to os.MkdirAll failed %+v: %w", dirs, err)
	}
	tmp, err := os.CreateTemp(dirs, ".cache-*")
	if err != nil {
		return fmt.Errorf("call to os.CreateTemp failed %q: %w", dirs, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = tmp.Write(value); err != nil {
		tmp.Close()
		return fmt.Errorf("call to tmp.Write failed %q: %w", path, err)
	}
	if err = tmp.Chmod(0644); err != nil {
		tmp.Close()
		return fmt.Errorf("call to tmp.Chmod failed %q: %w", path, err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("call to tmp.Close failed %q: %w", path, err)
	}
	err = os.Chtimes(tmpName, time.Now().UTC(), timestamp)
	if err != nil {
		return fmt.Errorf("call to os.Chtimes failed %q: %w", path, err)
	}
	err = os.Rename(tmpName, path)
	if os.IsNotExist(err) {
		// A concurrent Cleanup may have removed directories after MkdirAll above
		// so recreate them and retry the rename once
		if mkErr := os.MkdirAll(dirs, 0755); mkErr == nil {
			err = os.Rename(tmpName, path)
		}
	}
	if err != nil {
		return fmt.Errorf("call to os.Rename failed %q: %w", path, err)
	}
	return nil
}

// Keys returns distinct cache keys that have entries.
// In DiskStorage, each cache key is a directory inside BasePath.
func (c *DiskStorage) Keys() ([]string, error) {
	entries, err := os.ReadDir(c.BasePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("call to os.ReadDir failed %q: %w", c.BasePath, err)
	}
	var keys []string
	for _, entry := range entries {
		if entry.IsDir() {
			keys = append(keys, entry.Name())
		}
	}
	return keys, nil
}

// Clear will delete all cache entries
func (c *DiskStorage) Clear() error {
	err := os.RemoveAll(c.BasePath)
	if err != nil {
		return fmt.Errorf("failed to clear cache %q: when removing files: %w", c.BasePath, err)
	}
	err = os.Mkdir(c.BasePath, 0755)
	if err != nil {
		return fmt.Errorf("failed to clear cache %q: when recreating directory: %w", c.BasePath, err)
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
	// directories of entries that get removed, so they can be cleaned up
	// after the walk (see pruneEmptyDirs)
	var emptied []string
	c.iterateFiles(basePath, func(parent string, file fs.DirEntry) {
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
				return
			}
			emptied = append(emptied, parent)
		}
	})
	// removing files can leave a chain of empty directories behind, one per
	// entry, so remove them after the walk is finished
	pruneEmptyDirs(emptied, c.BasePath)
	return nil
}

// pruneEmptyDirs removes empty directories in dirs, plus any ancestor that become
// empty as a result. os.Remove fails when a directory holds entries, but this is
// not an error. It just means the directory is still in use.
func pruneEmptyDirs(dirs []string, stop string) {
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	sep := string(filepath.Separator)
	for _, dir := range dirs {
		for dir != stop && strings.HasPrefix(dir, stop+sep) {
			if err := os.Remove(dir); err != nil {
				break
			}
			dir = filepath.Dir(dir)
		}
	}
}

func (c *DiskStorage) EntryCount() (int64, error) {
	var count int64
	c.iterateFiles(c.BasePath, func(parent string, file fs.DirEntry) {
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
	c.iterateFiles(basePath, func(parent string, file fs.DirEntry) {
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

func (c *DiskStorage) iterateFiles(basePath string, callback func(string, fs.DirEntry)) {
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

// dump prints out a sample hexdump of cached content for test debugging
func (c *DiskStorage) dump(n int64) {
	var count int64
	basePath := c.BasePath
	if _, err := os.Stat(basePath); !os.IsNotExist(err) {
		c.iterateFiles(basePath, func(parent string, file fs.DirEntry) {
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
