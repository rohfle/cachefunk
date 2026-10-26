package cachefunk

import (
	"io"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// warningLogger is an atomic pointer because warnings read it from many
// goroutines while SetWarningLog and friends replace it.
var warningLogger atomic.Pointer[log.Logger]

// DefaultWarningInterval is how often the same warning is printed. A cache that
// cannot be written to would otherwise warn on every call. That is noise, and on
// a full disk it adds load to the disk.
const DefaultWarningInterval = time.Minute

// warningInterval holds the interval in nanoseconds; zero prints every warning
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

// WarningInterval returns the minimum interval between repeats of the same
// warning, or zero when warnings are not suppressed.
func WarningInterval() time.Duration {
	return time.Duration(warningInterval.Load())
}

// SetWarningInterval sets the minimum interval between repeats of the same
// warning. Warnings suppressed in between are counted and reported with the next
// warning. Zero or a negative duration prints every warning. SetWarningInterval
// is safe to call concurrently with warnings being printed.
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

// SetWarningLog sends warnings to logger instead of standard output. A nil logger
// is ignored. SetWarningLog is safe to call concurrently with warnings being
// printed.
func SetWarningLog(logger *log.Logger) {
	if logger != nil {
		warningLogger.Store(logger)
	}
}

// DisableWarnings discards warnings instead of printing them. DisableWarnings is
// safe to call concurrently with warnings being printed.
func DisableWarnings() {
	warningLogger.Store(log.New(io.Discard, "", 0))
}

// EnableWarnings sends warnings to standard output prefixed with "WARNING: ",
// which is the default. EnableWarnings is safe to call concurrently with warnings
// being printed.
func EnableWarnings() {
	warningLogger.Store(log.New(os.Stdout, "WARNING: ", log.LstdFlags))
}
