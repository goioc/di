package di

import (
	"log/slog"
	"os"
	"sync/atomic"
)

var defaultLogger = slog.New(slog.NewTextHandler(os.Stderr, nil))
var configuredLogger atomic.Pointer[slog.Logger]

// SetLogger configures the logger used for DI diagnostics. Its handler controls
// the output destination, format, and minimum level. Detailed lifecycle messages
// use slog.LevelDebug; registration warnings and cleanup errors use their
// corresponding levels.
//
// A nil logger restores the default text logger writing to stderr at Info level.
// The setting persists across [Close] and does not change [slog.Default] or the
// standard log package's configuration.
//
// SetLogger may be called concurrently with DI operations. Log calls already in
// progress may finish using the previous logger.
func SetLogger(logger *slog.Logger) {
	configuredLogger.Store(logger)
}

func currentLogger() *slog.Logger {
	if logger := configuredLogger.Load(); logger != nil {
		return logger
	}
	return defaultLogger
}
