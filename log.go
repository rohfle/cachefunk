package cachefunk

import (
	"io"
	"log"
	"os"
	"sync/atomic"
)

// warningLogger is read by every warning, so it is swapped atomically rather
// than assigned: warnings are printed from many goroutines and
// SetWarningLog/DisableWarnings/EnableWarnings can run at the same time.
var warningLogger atomic.Pointer[log.Logger]

func init() {
	EnableWarnings()
}

// warning prints a warning message using the current logger
func warning(fmtStr string, args ...any) {
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
