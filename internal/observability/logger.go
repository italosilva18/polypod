package observability

import (
	"log/slog"
	"os"
	"strings"
)

// NewLoggerToFile creates a logger that writes to the given file path.
func NewLoggerToFile(level, format, path string) (*slog.Logger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	return newLogger(level, format, f), nil
}

func NewLogger(level, format string) *slog.Logger {
	return newLogger(level, format, os.Stdout)
}

func newLogger(level, format string, w *os.File) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	if strings.ToLower(format) == "json" {
		handler = slog.NewJSONHandler(w, opts)
	} else {
		handler = slog.NewTextHandler(w, opts)
	}

	return slog.New(handler)
}
