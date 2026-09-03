package internal

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

func ConfigureLogLevelToFileAndStderr(logFilePath, logLevel string, stderr io.Writer) (io.Closer, error) {
	if err := os.MkdirAll(filepath.Dir(logFilePath), 0o755); err != nil {
		return nil, fmt.Errorf("create log directory, %w", err)
	}

	file, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	level := slog.LevelError
	switch strings.ToLower(logLevel) {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	}

	var (
		fileHandler   = slog.NewJSONHandler(io.MultiWriter(file), &slog.HandlerOptions{Level: level})
		stdErrHandler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	)
	handler := slog.NewMultiHandler(fileHandler, stdErrHandler)
	slog.SetDefault(slog.New(handler))
	return file, nil
}
