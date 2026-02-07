package util

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	once     sync.Once
	initDone bool
)

// LoggerConfig holds configuration for logger initialization.
type LoggerConfig struct {
	LogLevel   string
	LogFile    string
	NoColor    bool
	JSONFormat bool
}

// InitLogger initializes the global zerolog logger with the given configuration.
func InitLogger(config LoggerConfig) error {
	var err error
	once.Do(func() {
		err = initLoggerInternal(config)
		initDone = true
	})
	return err
}

func initLoggerInternal(config LoggerConfig) error {
	// Parse log level
	level, err := zerolog.ParseLevel(config.LogLevel)
	if err != nil {
		return err
	}
	zerolog.SetGlobalLevel(level)

	// Setup writers
	var writers []io.Writer

	// Console writer
	consoleWriter := zerolog.ConsoleWriter{
		Out:        os.Stdout,
		NoColor:    config.NoColor,
		TimeFormat: "2006-01-02T15:04:05.000Z07:00",
		FieldsExclude: []string{
			"caller",
			"goroutine",
		},
	}

	if config.JSONFormat {
		writers = append(writers, os.Stdout)
	} else {
		writers = append(writers, consoleWriter)
	}

	// File writer (if specified)
	if config.LogFile != "" {
		// Create directory if it doesn't exist
		logDir := filepath.Dir(config.LogFile)
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return err
		}

		logFile, err := os.OpenFile(config.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return err
		}

		// Use JSON format for file logs
		writers = append(writers, logFile)
	}

	// Multi-writer
	multiWriter := io.MultiWriter(writers...)

	// Configure global logger
	log.Logger = zerolog.New(multiWriter).
		With().
		Timestamp().
		Caller().
		Logger()

	// Set time field format to Unix timestamp for better performance
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs

	return nil
}

// GetLogger returns the configured logger instance.
func GetLogger() *zerolog.Logger {
	if !initDone {
		// Initialize with default config if not already initialized
		_ = InitLogger(LoggerConfig{
			LogLevel:   "info",
			NoColor:    false,
			JSONFormat: false,
		})
	}
	logger := log.Logger
	return &logger
}

// WithComponent returns a logger with the specified component field.
func WithComponent(component string) zerolog.Logger {
	return log.With().Str("component", component).Logger()
}

// WithField returns a logger with an additional field.
func WithField(key string, value any) zerolog.Logger {
	return log.With().Interface(key, value).Logger()
}

// WithFields returns a logger with multiple additional fields.
func WithFields(fields map[string]any) zerolog.Logger {
	ctx := log.With()
	for k, v := range fields {
		ctx = ctx.Interface(k, v)
	}
	return ctx.Logger()
}

// Debug logs a debug message with optional fields.
func Debug(msg string, fields ...map[string]any) {
	logMsg := log.Debug()
	if len(fields) > 0 {
		for k, v := range fields[0] {
			logMsg = logMsg.Interface(k, v)
		}
	}
	logMsg.Msg(msg)
}

// Info logs an info message with optional fields.
func Info(msg string, fields ...map[string]any) {
	logMsg := log.Info()
	if len(fields) > 0 {
		for k, v := range fields[0] {
			logMsg = logMsg.Interface(k, v)
		}
	}
	logMsg.Msg(msg)
}

// Warn logs a warning message with optional fields.
func Warn(msg string, fields ...map[string]any) {
	logMsg := log.Warn()
	if len(fields) > 0 {
		for k, v := range fields[0] {
			logMsg = logMsg.Interface(k, v)
		}
	}
	logMsg.Msg(msg)
}

// Error logs an error message with optional fields.
func Error(msg string, err error, fields ...map[string]any) {
	logMsg := log.Error()
	if err != nil {
		logMsg = logMsg.Err(err)
	}
	if len(fields) > 0 {
		for k, v := range fields[0] {
			logMsg = logMsg.Interface(k, v)
		}
	}
	logMsg.Msg(msg)
}

// Fatal logs a fatal message and exits the application.
func Fatal(msg string, err error, fields ...map[string]any) {
	logMsg := log.Fatal()
	if err != nil {
		logMsg = logMsg.Err(err)
	}
	if len(fields) > 0 {
		for k, v := range fields[0] {
			logMsg = logMsg.Interface(k, v)
		}
	}
	logMsg.Msg(msg)
}

// LogWithCaller logs a message with caller information.
func LogWithCaller(level string, msg string, skip int) {
	pc, file, line, ok := runtime.Caller(skip + 1)
	if !ok {
		return
	}

	fn := runtime.FuncForPC(pc)
	var fnName string
	if fn != nil {
		fnName = fn.Name()
	}

	fields := map[string]any{
		"caller": filepath.Base(file),
		"line":   line,
		"func":   fnName,
	}

	switch level {
	case "debug":
		Debug(msg, fields)
	case "info":
		Info(msg, fields)
	case "warn":
		Warn(msg, fields)
	case "error":
		Error(msg, nil, fields)
	case "fatal":
		Fatal(msg, nil, fields)
	}
}

// GetLogLevel returns the current log level.
func GetLogLevel() zerolog.Level {
	return zerolog.GlobalLevel()
}

// SetLogLevel dynamically changes the log level.
func SetLogLevel(level string) error {
	lvl, err := zerolog.ParseLevel(level)
	if err != nil {
		return err
	}
	zerolog.SetGlobalLevel(lvl)
	return nil
}
