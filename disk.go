package cachefunk

import (
	"context"
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

// DiskStorage stores cached items on disk in a tree of folders.
//
// A DiskStorage is safe for concurrent use by multiple goroutines. The zero value
// is not usable: call [NewDiskStorage].
//
// A cache key must not be blank, begin with a dot or contain ".."; the methods
// return an error for such a key.
//
// Reading or writing one file is not interruptible; the methods check ctx before
// starting, and walks stop between entries.
type DiskStorage struct {
	// BasePath is the root directory, with one folder per cache key beneath it.
	BasePath string
	// CalculatePath maps params to the path of the entry file, under the key's
	// folder.
	CalculatePath DiskStoragePather
}

// checkDirWritable returns an error when dirPath cannot be written to.
func checkDirWritable(dirPath string) error {
	// Creating a temp file is the most reliable way to test writability.
	tmpfile, err := os.CreateTemp(dirPath, "writable-test-")
	if err != nil {
		return fmt.Errorf("%q is not writable: %w", dirPath, err)
	}
	tmpfile.Close()
	os.Remove(tmpfile.Name())
	return nil
}

// NewDiskStorage returns a storage keeping entries under basePath, creating the
// directory if needed. pather maps params to each entry's relative path and
// defaults to DefaultDiskStoragePather when nil. It fails if basePath cannot be
// created or written to.
func NewDiskStorage(basePath string, pather DiskStoragePather) (*DiskStorage, error) {
	if pather == nil {
		pather = DefaultDiskStoragePather
	}

	basePath = filepath.Clean(basePath)
	if basePath == "" {
		return nil, errors.New("base path of DiskStorage should not be empty")
	}

	err := os.MkdirAll(basePath, 0755)
	if err != nil {
		return nil, fmt.Errorf("when creating base path of DiskStorage %q: %w", basePath, err)
	}

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

// Get reads the entry file, whose modification time is the timestamp. An expired
// entry is read only when FallbackToExpired is set; it stays in place for
// [DiskStorage.Cleanup] either way.
func (c *DiskStorage) Get(ctx context.Context, key string, config *KeyConfig, params string, expireBeforeTime time.Time) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := c.getCacheItemPath(key, config, params)
	if err != nil {
		return nil, fmt.Errorf("cache.Get: %w", err)
	}
	stat, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrEntryNotFound
		}
		return nil, fmt.Errorf("call to os.Stat failed %q: %w", path, err)
	}

	hasExpired := expireBeforeTime.After(stat.ModTime())
	if hasExpired && !config.FallbackToExpired {
		// No need to load expired data if not configured to fallback to expired
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
		// If FallbackToExpired is set, expired value will be used if the resolver fails.
		return value, ErrEntryExpired
	}
	return value, nil
}

// Set replaces the entry atomically: a concurrent reader sees the old value or
// the new one, never a partial write. The timestamp becomes the file's
// modification time.
func (c *DiskStorage) Set(ctx context.Context, key string, config *KeyConfig, params string, value []byte, timestamp time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
func (c *DiskStorage) Keys(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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

// Clear removes BasePath and recreates it, so anything else kept under the cache
// directory is removed too.
func (c *DiskStorage) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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

// Cleanup deletes the expired entry files under one key, then removes the
// directories they left empty.
func (c *DiskStorage) Cleanup(ctx context.Context, key string, config *KeyConfig, expireBeforeTime time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	basePath, err := c.keyPath(key)
	if err != nil {
		return fmt.Errorf("Cleanup: %w", err)
	}
	if _, err := os.Stat(basePath); os.IsNotExist(err) {
		return nil // key doesn't exist therefore nothing to do
	}
	// directories of entries that get removed, so they can be cleaned up
	// after the walk (see pruneEmptyDirs)
	var emptied []string
	var removed int
	c.iterateFiles(ctx, basePath, func(parent string, file fs.DirEntry) {
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
			removed++
			emptied = append(emptied, parent)
		}
	})
	// removing files can leave a chain of empty directories behind, one per
	// entry, so remove them after the walk is finished
	pruneEmptyDirs(emptied, c.BasePath)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("cleanup cancelled after removing %d entries: %w", removed, err)
	}
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

// EntryCount returns the number of files beneath BasePath.
func (c *DiskStorage) EntryCount(ctx context.Context) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var count int64
	c.iterateFiles(ctx, c.BasePath, func(parent string, file fs.DirEntry) {
		count += 1
	})
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return count, nil
}

// ExpiredEntryCount returns the number of expired entry files under one key.
func (c *DiskStorage) ExpiredEntryCount(ctx context.Context, key string, config *KeyConfig, expireBeforeTime time.Time) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var count int64
	basePath, err := c.keyPath(key)
	if err != nil {
		return 0, fmt.Errorf("ExpiredEntryCount: %w", err)
	}
	if _, err := os.Stat(basePath); os.IsNotExist(err) {
		return 0, nil // key doesn't exist therefore count is 0
	}
	c.iterateFiles(ctx, basePath, func(parent string, file fs.DirEntry) {
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
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return count, nil
}

func (c *DiskStorage) iterateFiles(ctx context.Context, basePath string, callback func(string, fs.DirEntry)) {
	dirsLeft := []string{basePath}
	var curDir string
	for len(dirsLeft) > 0 {
		if ctx.Err() != nil {
			return
		}
		curDir, dirsLeft = dirsLeft[0], dirsLeft[1:]
		entries, err := os.ReadDir(curDir)
		if err != nil {
			warning("skipping %q: failed to read directory: %v", curDir, err)
			continue
		}

		for _, entry := range entries {
			if ctx.Err() != nil {
				return
			}
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
		c.iterateFiles(context.Background(), basePath, func(parent string, file fs.DirEntry) {
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
