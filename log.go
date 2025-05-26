package cachefunk

import (
	"io"
	"log"
	"os"
)

// Default to standard output logger
var warningLogger *log.Logger = log.New(os.Stdout, "WARNING: ", log.LstdFlags)

// warning prints a warning message using the current logger
func warning(fmtStr string, args ...any) {
	warningLogger.Printf(fmtStr, args...)
}

// SetWarningLog allows redirection of warning output
func SetWarningLog(logger *log.Logger) {
	if logger != nil {
		warningLogger = logger
	}
}

// Disable warnings
func DisableWarnings() {
	warningLogger = log.New(io.Discard, "", 0)
}

// Enable warnings
func EnableWarnings() {
	warningLogger = log.New(os.Stdout, "WARNING: ", log.LstdFlags)
}
