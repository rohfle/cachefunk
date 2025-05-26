package cachefunk_test

import (
	"log"
	"os"
	"testing"

	"github.com/rohfle/cachefunk"
)

func TestLog(t *testing.T) {
	cachefunk.SetWarningLog(log.New(os.Stdout, "WARNING: ", log.LstdFlags))
}
