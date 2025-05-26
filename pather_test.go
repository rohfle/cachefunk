package cachefunk_test

import (
	"testing"

	"github.com/rohfle/cachefunk"
)

func TestPather(t *testing.T) {
	cachefunk.SHA256HexPather("thisiskey", "thisisparams")
	cachefunk.SHA256Base64Pather("thisiskey", "thisisparams")
}
