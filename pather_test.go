package cachefunk

import (
	"testing"
)

func TestPather(t *testing.T) {
	SHA256HexPather("thisisparams")
	SHA256Base64Pather("thisisparams")
}
