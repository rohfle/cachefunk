package cachefunk

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// Returns a path with prefix directories
type DiskStoragePather func(cacheKey string, params string) []string

var DefaultDiskStoragePather = SHA256HexPather

func SHA256HexPather(cacheKey string, params string) []string {
	data := sha256.Sum256([]byte(params))
	hash := hex.EncodeToString(data[:])
	return []string{cacheKey, hash[0:2], hash[2:4], hash[4:6], hash[6:]}
}

func SHA256Base64Pather(cacheKey string, params string) []string {
	data := sha256.Sum256([]byte(params))
	hash := base64.URLEncoding.EncodeToString(data[:])
	return []string{cacheKey, hash[0:2], hash[2:4], hash}
}
