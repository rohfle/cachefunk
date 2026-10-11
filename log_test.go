package cachefunk

import (
	"io"
	"log"
	"os"
	"sync"
	"testing"
)

func TestLog(t *testing.T) {
	SetWarningLog(log.New(os.Stdout, "WARNING: ", log.LstdFlags))
}

// TestLogConcurrent checks that warning output can be redirected while warnings
// are being printed. Its real value is under `go test -race`: assigning the
// logger variable instead of swapping it atomically is reported as a data race.
func TestLogConcurrent(t *testing.T) {
	discard := func() *log.Logger { return log.New(io.Discard, "", 0) }
	SetWarningLog(discard())

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 1000 {
			warning("concurrent warning %d", 1)
		}
	}()
	go func() {
		defer wg.Done()
		for range 1000 {
			SetWarningLog(discard())
			DisableWarnings()
		}
	}()
	wg.Wait()

	// leave the package default in place for the other tests
	EnableWarnings()
}
