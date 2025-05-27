package cachefunk

import (
	"testing"
)

func TestPather(t *testing.T) {
	SHA256HexPather("thisiskey", "thisisparams")
	SHA256Base64Pather("thisiskey", "thisisparams")
}
