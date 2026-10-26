package cachefunk

import (
	"bytes"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLog(t *testing.T) {
	SetWarningLog(log.New(os.Stdout, "WARNING: ", log.LstdFlags))
}

// TestWarningLatch checks that a repeated warning prints once per interval with
// the number suppressed, and that a different warning is not suppressed by it. A
// zero interval prints every warning.
func TestWarningLatch(t *testing.T) {
	var logged bytes.Buffer
	SetWarningLog(log.New(&logged, "", 0))
	SetWarningInterval(time.Hour)
	t.Cleanup(func() {
		EnableWarnings()
		SetWarningInterval(DefaultWarningInterval)
	})

	const repeated = "latch test repeated warning %d"
	warning(repeated, 1)
	warning(repeated, 2)
	warning(repeated, 3)

	lines := strings.Split(strings.TrimSpace(logged.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected one printed warning but got %d: %q", len(lines), logged.String())
	}
	if strings.Contains(lines[0], "suppressed") {
		t.Errorf("did not expect a suppressed count on the first warning: %q", lines[0])
	}

	// rewind the latch instead of sleeping for the interval: the next warning
	// reports the two that were suppressed
	value, ok := warningLatches.Load(repeated)
	if !ok {
		t.Fatal("expected a latch for the repeated warning")
	}
	latch := value.(*warningLatch)
	latch.mutex.Lock()
	latch.next = time.Time{}
	latch.mutex.Unlock()

	warning(repeated, 4)
	lines = strings.Split(strings.TrimSpace(logged.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected the rewound latch to print again, got %d lines: %q", len(lines), logged.String())
	}
	if !strings.Contains(lines[1], "(2 suppressed)") {
		t.Errorf("expected the suppressed count on the next warning, got %q", lines[1])
	}

	// the count starts over once it has been reported
	warning(repeated, 5)
	if got := len(strings.Split(strings.TrimSpace(logged.String()), "\n")); got != 2 {
		t.Errorf("expected the warning since the last report to be suppressed, got %d lines: %q", got, logged.String())
	}

	// a different format string is latched separately, so one failing path cannot
	// hide another
	warning("latch test other warning %d", 1)
	if got := len(strings.Split(strings.TrimSpace(logged.String()), "\n")); got != 3 {
		t.Errorf("expected the other warning to be printed, got %d lines: %q", got, logged.String())
	}

	// with no interval, every warning prints
	SetWarningInterval(0)
	warning(repeated, 6)
	warning(repeated, 7)
	if got := len(strings.Split(strings.TrimSpace(logged.String()), "\n")); got != 5 {
		t.Errorf("expected every warning to be printed with a zero interval, got %d lines: %q", got, logged.String())
	}
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
