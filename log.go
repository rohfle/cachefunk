package cachefunk

import (
	"io"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// warningLogger is read by every warning, so it is swapped atomically rather
// than assigned: warnings are printed from many goroutines and
// SetWarningLog/DisableWarnings/EnableWarnings can run at the same time.
var warningLogger atomic.Pointer[log.Logger]

// DefaultWarningInterval is how often the same warning is printed. A cache that
// cannot be written to warns on every call, which is both noise and, when the
// cause is a full disk, more pressure on it.
const DefaultWarningInterval = time.Minute

// warningInterval is the current interval in nanoseconds, zero to print every
// warning
var warningInterval atomic.Int64

// warningLatches holds one latch per warning format string, so repeated warnings
// about the same problem are suppressed without suppressing a different one
var warningLatches sync.Map

type warningLatch struct {
	mutex      sync.Mutex
	next       time.Time
	suppressed int64
}

func init() {
	warningInterval.Store(int64(DefaultWarningInterval))
	EnableWarnings()
}

// WarningInterval returns how often the same warning should be printed, or zero
// when every warning should be printed
func WarningInterval() time.Duration {
	return time.Duration(warningInterval.Load())
}

// SetWarningInterval sets how often the same warning is printed. Warnings
// suppressed in between are counted and reported upon the next warning.
// Zero or a negative duration prints every warning.
func SetWarningInterval(interval time.Duration) {
	warningInterval.Store(int64(interval))
}

// warning prints a message using the current logger, at most once per
// warning interval per unique format string
func warning(fmtStr string, args ...any) {
	if interval := WarningInterval(); interval > 0 {
		value, _ := warningLatches.LoadOrStore(fmtStr, &warningLatch{})
		latch := value.(*warningLatch)
		latch.mutex.Lock()
		now := time.Now()
		if now.Before(latch.next) {
			latch.suppressed++
			latch.mutex.Unlock()
			return
		}
		suppressed := latch.suppressed
		latch.suppressed = 0
		latch.next = now.Add(interval)
		latch.mutex.Unlock()
		if suppressed > 0 {
			// the caller's args are not ours to extend, so cap the slice
			fmtStr += " (%d suppressed)"
			args = append(args[:len(args):len(args)], suppressed)
		}
	}
	warningLogger.Load().Printf(fmtStr, args...)
}

// SetWarningLog allows redirection of warning output
func SetWarningLog(logger *log.Logger) {
	if logger != nil {
		warningLogger.Store(logger)
	}
}

// DisableWarnings stops warnings from being printed
func DisableWarnings() {
	warningLogger.Store(log.New(io.Discard, "", 0))
}

// EnableWarnings prints warnings to standard output, which is the default
func EnableWarnings() {
	warningLogger.Store(log.New(os.Stdout, "WARNING: ", log.LstdFlags))
}
