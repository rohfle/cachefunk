package cachefunk

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// DiskStoragePather returns a path representing the key and params.
// The path is broken down into an array of directories to help limit
// file counts in folders when there are thousands of cache entries.
type DiskStoragePather func(params string) []string

var DefaultDiskStoragePather = SHA256HexPather

func SHA256HexPather(params string) []string {
	data := sha256.Sum256([]byte(params))
	hash := hex.EncodeToString(data[:])
	return []string{hash[0:2], hash[2:4], hash[4:6], hash[6:]}
}

func SHA256Base64Pather(params string) []string {
	data := sha256.Sum256([]byte(params))
	hash := base64.URLEncoding.EncodeToString(data[:])
	return []string{hash[0:2], hash[2:4], hash}
}
