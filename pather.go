package cachefunk

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// DiskStoragePather returns the entry path for params. The path is split into
// directories to limit the file count in any one folder when there are thousands
// of cache entries.
type DiskStoragePather func(params string) []string

// DefaultDiskStoragePather is [SHA256HexPather]. [NewDiskStorage] uses it when no
// pather is given.
var DefaultDiskStoragePather = SHA256HexPather

// SHA256HexPather hashes params with SHA-256 and splits the hex digest into four
// path components: three two-character directories and the rest as the file
// name. The fixed digest length keeps paths short, and the fan-out keeps any one
// directory manageable.
func SHA256HexPather(params string) []string {
	data := sha256.Sum256([]byte(params))
	hash := hex.EncodeToString(data[:])
	return []string{hash[0:2], hash[2:4], hash[4:6], hash[6:]}
}

// SHA256Base64Pather is SHA256HexPather with a shorter path: two two-character
// directories and the base64 digest as the file name. The hash is not reversible
// either way, only its spelling differs.
func SHA256Base64Pather(params string) []string {
	data := sha256.Sum256([]byte(params))
	hash := base64.URLEncoding.EncodeToString(data[:])
	return []string{hash[0:2], hash[2:4], hash}
}
