package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// LogLevel represents logging levels
type LogLevel string

// Log levels
const (
	DebugLevel LogLevel = "debug"
	InfoLevel  LogLevel = "info"
	WarnLevel  LogLevel = "warn"
	ErrorLevel LogLevel = "error"
	FatalLevel LogLevel = "fatal"
	PanicLevel LogLevel = "panic"
)

// TimeFormat defines standard time formats for logging
type TimeFormat string

// Available time formats
const (
	TimeFormatISO8601   TimeFormat = "iso8601"   // 2006-01-02T15:04:05.000Z0700
	TimeFormatUnix      TimeFormat = "unix"      // Unix timestamp
	TimeFormatUnixMilli TimeFormat = "unixmilli" // Unix timestamp with milliseconds
	TimeFormatRFC3339   TimeFormat = "rfc3339"   // RFC3339 format
	TimeFormatSimple    TimeFormat = "simple"    // 2006-01-02 15:04:05
	TimeFormatKitchen   TimeFormat = "kitchen"   // 3:04:05PM
)

// OutputFormat defines the log output format
type OutputFormat string

// Available output formats
const (
	FormatJSON    OutputFormat = "json"
	FormatConsole OutputFormat = "console"
	FormatPretty  OutputFormat = "pretty"  // Colored, human-friendly output
	FormatCompact OutputFormat = "compact" // Minimal output format
)

// Config contains logger configuration options
type Config struct {
	Level              LogLevel
	Format             OutputFormat
	TimeFormat         TimeFormat
	Output             string // stdout, stderr, or file path
	Development        bool
	AddCallerInfo      bool
	CallerSkip         int      // How many levels of stack to skip when capturing caller
	StackTrace         bool     // Include stack traces for errors
	ServiceName        string   // Name of service for metadata
	Version            string   // Version of software for metadata
	Environment        string   // Environment (production, staging, etc)
	SamplingEnabled    bool     // Enable log sampling to reduce volume
	SamplingInitial    int      // Initial sampling allowance
	SamplingThereafter int      // Sampling rate after initial allowance
	ContextualFields   []string // Additional contextual fields to always include
	RedactFields       []string // Fields to redact from logs (e.g. "password", "token")
}

// Logger wraps zap logger with additional functionality
type Logger struct {
	*zap.SugaredLogger
	config Config
	fields map[string]interface{}
	level  zap.AtomicLevel
}

// Default config values
var defaultConfig = Config{
	Level:              InfoLevel,
	Format:             FormatPretty,
	TimeFormat:         TimeFormatISO8601,
	Output:             "stdout",
	Development:        false,
	AddCallerInfo:      true,
	CallerSkip:         0,
	StackTrace:         true,
	ServiceName:        "app",
	Version:            "unknown",
	Environment:        "development",
	SamplingEnabled:    false,
	SamplingInitial:    100,
	SamplingThereafter: 100,
	ContextualFields:   []string{},
	RedactFields:       []string{"password", "secret", "token", "key", "auth"},
}

// New creates a new logger with default configuration
func New() *Logger {
	return NewWithConfig(defaultConfig)
}

// TimeEncoder returns an encoding function for timestamps based on the format
func TimeEncoder(format TimeFormat) zapcore.TimeEncoder {
	switch format {
	case TimeFormatISO8601:
		return zapcore.ISO8601TimeEncoder
	case TimeFormatUnix:
		return zapcore.EpochTimeEncoder
	case TimeFormatUnixMilli:
		return zapcore.EpochMillisTimeEncoder
	case TimeFormatRFC3339:
		return func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
			enc.AppendString(t.Format(time.RFC3339))
		}
	case TimeFormatSimple:
		return func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
			enc.AppendString(t.Format("2006-01-02 15:04:05"))
		}
	case TimeFormatKitchen:
		return func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
			enc.AppendString(t.Format(time.Kitchen))
		}
	default:
		return zapcore.ISO8601TimeEncoder
	}
}

// NewWithConfig creates a new logger with the specified configuration
func NewWithConfig(config Config) *Logger {
	level := getZapLevel(config.Level)
	atomicLevel := zap.NewAtomicLevelAt(level)

	// Configure encoder
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "message",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     TimeEncoder(config.TimeFormat),
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	// Customize encoder based on format
	switch config.Format {
	case FormatConsole:
		encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		encoderConfig.EncodeCaller = func(caller zapcore.EntryCaller, enc zapcore.PrimitiveArrayEncoder) {
			// Get short file path
			_, file := filepath.Split(caller.File)
			enc.AppendString(fmt.Sprintf("%s:%d", file, caller.Line))
		}
	case FormatPretty:
		encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		encoderConfig.EncodeCaller = func(caller zapcore.EntryCaller, enc zapcore.PrimitiveArrayEncoder) {
			// Get short file path with parent directory for better context
			dir, file := filepath.Split(caller.File)
			parentDir := filepath.Base(dir)
			enc.AppendString(fmt.Sprintf("%s/%s:%d", parentDir, file, caller.Line))
		}
		encoderConfig.ConsoleSeparator = " | "
	case FormatCompact:
		// Minimal format with just essentials
		encoderConfig.TimeKey = "" // Skip time to keep it short
		encoderConfig.LevelKey = "l"
		encoderConfig.MessageKey = "m"
		encoderConfig.CallerKey = "c"
		encoderConfig.EncodeLevel = func(l zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
			// Single letter level indicators
			switch l {
			case zapcore.DebugLevel:
				enc.AppendString("D")
			case zapcore.InfoLevel:
				enc.AppendString("I")
			case zapcore.WarnLevel:
				enc.AppendString("W")
			case zapcore.ErrorLevel:
				enc.AppendString("E")
			case zapcore.FatalLevel:
				enc.AppendString("F")
			case zapcore.PanicLevel:
				enc.AppendString("P")
			}
		}
	}

	// Configure output
	var output zapcore.WriteSyncer
	switch strings.ToLower(config.Output) {
	case "stdout":
		output = zapcore.AddSync(os.Stdout)
	case "stderr":
		output = zapcore.AddSync(os.Stderr)
	default:
		// Create directory if needed
		dir := filepath.Dir(config.Output)
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create log directory %s: %v\n", dir, err)
		}

		// Assume it's a file path
		file, err := os.OpenFile(config.Output, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to open log file %s: %v\n", config.Output, err)
			output = zapcore.AddSync(os.Stderr)
		} else {
			output = zapcore.AddSync(file)
		}
	}

	// Configure encoder format
	var encoder zapcore.Encoder
	switch config.Format {
	case FormatJSON:
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	case FormatConsole, FormatPretty, FormatCompact:
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	default:
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	}

	// Configure sampling if enabled
	var core zapcore.Core
	if config.SamplingEnabled {
		core = zapcore.NewSamplerWithOptions(
			zapcore.NewCore(encoder, output, atomicLevel),
			time.Second,
			config.SamplingInitial,
			config.SamplingThereafter,
		)
	} else {
		core = zapcore.NewCore(encoder, output, atomicLevel)
	}

	// Add options
	opts := []zap.Option{}
	if config.AddCallerInfo {
		opts = append(opts, zap.AddCaller())
		if config.CallerSkip != 0 {
			opts = append(opts, zap.AddCallerSkip(config.CallerSkip))
		}
	}
	if config.Development {
		opts = append(opts, zap.Development())
	}
	if config.StackTrace {
		opts = append(opts, zap.AddStacktrace(zapcore.ErrorLevel))
	}

	// Create logger with initial fields
	initialFields := map[string]interface{}{
		"service": config.ServiceName,
	}

	if config.Environment != "" {
		initialFields["env"] = config.Environment
	}

	if config.Version != "" {
		initialFields["version"] = config.Version
	}

	// Add hostname for better identification
	hostname, err := os.Hostname()
	if err == nil && hostname != "" {
		initialFields["host"] = hostname
	}

	// Create logger
	zapLogger := zap.New(core, opts...)

	// Add initial fields directly
	zapFields := make([]zap.Field, 0, len(initialFields))
	for k, v := range initialFields {
		zapFields = append(zapFields, zap.Any(k, v))
	}
	sugar := zapLogger.With(zapFields...).Sugar()

	return &Logger{
		SugaredLogger: sugar,
		config:        config,
		fields:        initialFields,
		level:         atomicLevel,
	}
}

// fieldsToArgs converts a fields map to a slice of alternating keys and values
func fieldsToArgs(fields map[string]interface{}) []interface{} {
	args := make([]interface{}, 0, len(fields)*2)
	for k, v := range fields {
		args = append(args, k, v)
	}
	return args
}

// getZapLevel converts our LogLevel to zap's level
func getZapLevel(level LogLevel) zapcore.Level {
	switch strings.ToLower(string(level)) {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	case "fatal":
		return zapcore.FatalLevel
	case "panic":
		return zapcore.PanicLevel
	default:
		return zapcore.InfoLevel
	}
}

// WithField returns a logger with a field added to it
func (l *Logger) WithField(key string, value interface{}) *Logger {
	// Check if this is a field that should be redacted
	if l.shouldRedact(key) {
		value = "[REDACTED]"
	}

	newFields := make(map[string]interface{}, len(l.fields)+1)
	for k, v := range l.fields {
		newFields[k] = v
	}
	newFields[key] = value

	return &Logger{
		SugaredLogger: l.SugaredLogger.With(key, value),
		config:        l.config,
		fields:        newFields,
		level:         l.level,
	}
}

// shouldRedact checks if a field should be redacted
func (l *Logger) shouldRedact(key string) bool {
	lowerKey := strings.ToLower(key)
	for _, f := range l.config.RedactFields {
		if strings.Contains(lowerKey, strings.ToLower(f)) {
			return true
		}
	}
	return false
}

// WithFields returns a logger with multiple fields added to it
func (l *Logger) WithFields(fields map[string]interface{}) *Logger {
	newFields := make(map[string]interface{}, len(l.fields)+len(fields))

	// Copy existing fields
	for k, v := range l.fields {
		newFields[k] = v
	}

	// Process and add new fields
	processedFields := make(map[string]interface{}, len(fields))
	for k, v := range fields {
		if l.shouldRedact(k) {
			processedFields[k] = "[REDACTED]"
		} else {
			processedFields[k] = v
		}
		newFields[k] = processedFields[k]
	}

	return &Logger{
		SugaredLogger: l.SugaredLogger.With(fieldsToArgs(processedFields)...),
		config:        l.config,
		fields:        newFields,
		level:         l.level,
	}
}

// WithError returns a logger with an error field and additional error context
func (l *Logger) WithError(err error) *Logger {
	if err == nil {
		return l
	}

	// Create fields from error
	errorFields := map[string]interface{}{
		"error": err.Error(),
	}

	// Add error type for better classification
	errorFields["error_type"] = fmt.Sprintf("%T", err)

	return l.WithFields(errorFields)
}

// WithContext enriches the logger with contextual information from the call site
func (l *Logger) WithContext() *Logger {
	_, file, line, ok := runtime.Caller(1)
	if !ok {
		return l
	}

	// Add caller info as fields for consistent inclusion
	fields := map[string]interface{}{
		"file": file,
		"line": line,
	}

	// Get package and function name
	if pc, _, _, ok := runtime.Caller(1); ok {
		if fn := runtime.FuncForPC(pc); fn != nil {
			fullName := fn.Name()
			if lastSlash := strings.LastIndexByte(fullName, '/'); lastSlash != -1 {
				if lastDot := strings.LastIndexByte(fullName[lastSlash:], '.'); lastDot != -1 {
					fields["function"] = fullName[lastSlash+lastDot+1:]
					fields["package"] = fullName[:lastSlash+lastDot]
				}
			} else {
				fields["function"] = fullName
			}
		}
	}

	return l.WithFields(fields)
}

// SetLevel changes the logging level dynamically
func (l *Logger) SetLevel(level LogLevel) {
	l.level.SetLevel(getZapLevel(level))
	l.Infof("Log level changed to %s", level)
}

// GetLevel returns the current logging level
func (l *Logger) GetLevel() LogLevel {
	zapLevel := l.level.Level()

	switch zapLevel {
	case zapcore.DebugLevel:
		return DebugLevel
	case zapcore.InfoLevel:
		return InfoLevel
	case zapcore.WarnLevel:
		return WarnLevel
	case zapcore.ErrorLevel:
		return ErrorLevel
	case zapcore.FatalLevel:
		return FatalLevel
	case zapcore.PanicLevel:
		return PanicLevel
	default:
		return InfoLevel
	}
}

// Debug logs a debug message with optional key-value pairs
func (l *Logger) Debug(msg string, keysAndValues ...interface{}) {
	l.SugaredLogger.Debugw(msg, keysAndValues...)
}

// Info logs an info message with optional key-value pairs
func (l *Logger) Info(msg string, keysAndValues ...interface{}) {
	l.SugaredLogger.Infow(msg, keysAndValues...)
}

// Warn logs a warning message with optional key-value pairs
func (l *Logger) Warn(msg string, keysAndValues ...interface{}) {
	l.SugaredLogger.Warnw(msg, keysAndValues...)
}

// Error logs an error message with optional key-value pairs
func (l *Logger) Error(msg string, keysAndValues ...interface{}) {
	l.SugaredLogger.Errorw(msg, keysAndValues...)
}

// Fatal logs a fatal message with optional key-value pairs and then exits
func (l *Logger) Fatal(msg string, err error) {
	if err != nil {
		l.SugaredLogger.Fatalw(msg, "error", err)
	} else {
		l.SugaredLogger.Fatalw(msg)
	}
}

// Panic logs a panic message with optional key-value pairs and then panics
func (l *Logger) Panic(msg string, keysAndValues ...interface{}) {
	l.SugaredLogger.Panicw(msg, keysAndValues...)
}

// HTTPRequest logs an HTTP request with detailed information
func (l *Logger) HTTPRequest(method, path string, status int, latency time.Duration, keysAndValues ...interface{}) {
	// Combine standard HTTP fields with any additional fields
	fields := make([]interface{}, 0, 8+len(keysAndValues))
	fields = append(fields,
		"method", method,
		"path", path,
		"status", status,
		"latency_ms", latency.Milliseconds(),
	)
	fields = append(fields, keysAndValues...)

	// Color code based on status
	if status >= 500 {
		l.SugaredLogger.Errorw("HTTP Request", fields...)
	} else if status >= 400 {
		l.SugaredLogger.Warnw("HTTP Request", fields...)
	} else {
		l.SugaredLogger.Infow("HTTP Request", fields...)
	}
}

// TraceError logs an error with automatic stack trace and context
func (l *Logger) TraceError(msg string, err error) {
	if err == nil {
		return
	}

	_, file, line, _ := runtime.Caller(1)
	fields := []interface{}{
		"error", err.Error(),
		"error_type", fmt.Sprintf("%T", err),
		"file", file,
		"line", line,
	}

	l.SugaredLogger.Errorw(msg, fields...)
}

// GetConfig returns the logger's configuration
func (l *Logger) GetConfig() Config {
	return l.config
}

// NewTestLogger creates a logger suitable for testing with pretty output
func NewTestLogger() *Logger {
	config := defaultConfig
	config.Level = DebugLevel
	config.Format = FormatPretty
	config.TimeFormat = TimeFormatSimple
	config.Development = true
	config.ServiceName = "test"
	return NewWithConfig(config)
}

// NewPrettyConsoleLogger creates a colorful, human-friendly logger for development
func NewPrettyConsoleLogger() *Logger {
	config := defaultConfig
	config.Format = FormatPretty
	config.TimeFormat = TimeFormatSimple
	config.Development = true
	return NewWithConfig(config)
}

// NewProductionLogger creates a logger optimized for production use
func NewProductionLogger(serviceName, version, environment string) *Logger {
	config := defaultConfig
	config.Format = FormatJSON
	config.TimeFormat = TimeFormatISO8601
	config.Development = false
	config.SamplingEnabled = true
	config.ServiceName = serviceName
	config.Version = version
	config.Environment = environment
	return NewWithConfig(config)
}

// GetZapLogger returns the underlying zap.Logger
func (l *Logger) GetZapLogger() *zap.Logger {
	return l.SugaredLogger.Desugar()
}
