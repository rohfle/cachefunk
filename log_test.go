package cachefunk

import (
	"log"
	"os"
	"testing"
)

func TestLog(t *testing.T) {
	SetWarningLog(log.New(os.Stdout, "WARNING: ", log.LstdFlags))
}
