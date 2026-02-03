package logger

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// ANSI color codes for terminal output
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
)

// LogLevel represents the severity level of log messages, providing a hierarchical
// filtering mechanism where higher levels control message visibility and ensure
// appropriate logging verbosity across application components for debugging,
// monitoring, and production operations.
//
// Level hierarchy (lowest to highest):
//   - DEBUG: Verbose debugging information
//   - WARN: Warning conditions
//   - ERROR: Error conditions
//   - INFO: Informational notices (always shown unless NONE)
//   - NONE: Disable all logging
type LogLevel int

const (
	DEBUG LogLevel = iota
	WARN
	ERROR
	INFO // INFO sits just below NONE - always shows unless logging is completely disabled
	NONE
)

// LogEntry represents a single log message with associated metadata including
// timestamp, severity level, and message content. This structure provides the
// foundation for log buffering and retrieval through administrative interfaces,
// enabling real-time monitoring and historical analysis of application behavior.
type LogEntry struct {
	Timestamp string `json:"timestamp"` // RFC 3339 formatted timestamp for log entry creation
	Level     string `json:"level"`     // Lowercase severity level (debug, info, warn, error)
	Message   string `json:"message"`   // Complete formatted log message content
}

var (
	// defaultLogger provides the singleton logger instance used by package-level
	// logging functions, ensuring consistent configuration and behavior across
	// the application without requiring explicit logger management.
	defaultLogger *Logger

	// once ensures thread-safe singleton initialization of the default logger
	// instance, preventing race conditions during concurrent package initialization
	// across multiple goroutines in application startup sequences.
	once sync.Once

	// logBuffer maintains a circular buffer of recent log entries with automatic
	// size management, providing efficient memory usage while preserving sufficient
	// historical context for debugging and administrative log review operations.
	logBuffer []LogEntry

	// logMutex protects concurrent access to the log buffer during write operations
	// from multiple goroutines and read operations from administrative API endpoints,
	// ensuring thread-safe log management without data races or corruption.
	logMutex sync.RWMutex

	// maxLogEntries defines the maximum number of log entries retained in the
	// circular buffer before oldest entries are discarded, balancing memory usage
	// with administrative visibility into recent application behavior and events.
	maxLogEntries = 1000

	// levelColors maps log levels to their ANSI color codes for terminal output
	levelColors = map[string]string{
		"DEBUG": colorGreen,
		"WARN":  colorYellow,
		"ERROR": colorRed,
		"INFO":  colorBlue,
	}
)

// Logger provides leveled logging functionality with thread-safe configuration
// management and filtering capabilities. The logger supports both instance-based
// usage for component-specific logging and package-level convenience functions
// for general application logging needs.
type Logger struct {
	level LogLevel     // Current minimum log level for message filtering
	mu    sync.RWMutex // Protects level changes during runtime reconfiguration
}

// New creates a new Logger instance with the specified log level, enabling
// component-specific logging configuration and filtering independent of the
// global default logger used by package-level logging functions.
//
// Parameters:
//   - level: string representation of log level (DEBUG, INFO, WARN, ERROR, NONE)
//
// Returns:
//   - *Logger: configured logger instance ready for use
func New(level string) *Logger {
	return &Logger{
		level: ParseLogLevel(level),
	}
}

// getDefaultLogger returns the singleton default logger instance, initializing
// it on first access with INFO level as the default. This function ensures
// thread-safe initialization and consistent logger behavior across package-level
// logging operations throughout the application lifecycle.
func getDefaultLogger() *Logger {
	once.Do(func() {
		defaultLogger = &Logger{
			level: INFO,
		}
	})
	return defaultLogger
}

// ParseLogLevel converts string log level representations to LogLevel constants,
// providing case-insensitive parsing with fallback to INFO level for unknown
// values. This function ensures robust configuration handling and prevents
// invalid log level specifications from causing runtime errors.
//
// Parameters:
//   - level: string representation of log level
//
// Returns:
//   - LogLevel: parsed log level constant
func ParseLogLevel(level string) LogLevel {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return DEBUG
	case "WARN", "WARNING":
		return WARN
	case "ERROR":
		return ERROR
	case "INFO":
		return INFO
	case "NONE":
		return NONE
	default:
		return INFO
	}
}

// SetLogLevel updates the global default logger's minimum level for message
// filtering, providing runtime log level adjustment without application restart.
// This function enables dynamic logging verbosity control for debugging and
// troubleshooting operations.
//
// Parameters:
//   - level: string representation of new log level
func SetLogLevel(level string) {
	getDefaultLogger().SetLevel(level)
}

// GetLogLevel retrieves the current global default logger's level as a string
// representation, enabling configuration introspection and administrative
// interface display of current logging verbosity settings.
//
// Returns:
//   - string: current log level as uppercase string
func GetLogLevel() string {
	return getDefaultLogger().GetLevel()
}

// SetLevel updates this logger instance's minimum level for message filtering
// with thread-safe locking to prevent race conditions during concurrent
// configuration changes and logging operations.
//
// Parameters:
//   - level: string representation of new log level
func (l *Logger) SetLevel(level string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = ParseLogLevel(level)
}

// GetLevel retrieves this logger instance's current level as a string
// representation with thread-safe read locking to ensure consistent
// configuration access during concurrent operations.
//
// Returns:
//   - string: current log level as uppercase string
func (l *Logger) GetLevel() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	switch l.level {
	case DEBUG:
		return "DEBUG"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	case INFO:
		return "INFO"
	case NONE:
		return "NONE"
	default:
		return "INFO"
	}
}

// shouldLog determines whether a message at the specified level should be
// logged based on the logger's current minimum level configuration, implementing
// hierarchical filtering where messages below the threshold are silently discarded
// for performance optimization.
//
// Parameters:
//   - level: severity level of message being evaluated
//
// Returns:
//   - bool: true if message should be logged, false if filtered
func (l *Logger) shouldLog(level LogLevel) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()

	// NONE means no logging - either for the logger level or message level
	if l.level == NONE || level == NONE {
		return false
	}

	return level >= l.level
}

// addToBuffer appends a log entry to the circular buffer with automatic size
// management and thread-safe concurrent access protection. The function maintains
// the most recent maxLogEntries entries by discarding oldest entries when the
// buffer exceeds capacity.
//
// Parameters:
//   - level: severity level string for buffer entry
//   - message: formatted message content for buffer entry
func addToBuffer(level, message string) {
	// Don't add to buffer if level is NONE
	if strings.ToUpper(level) == "NONE" {
		return
	}

	logMutex.Lock()
	defer logMutex.Unlock()

	entry := LogEntry{
		Timestamp: time.Now().Format("2006-01-02 15:04:05"),
		Level:     strings.ToLower(level),
		Message:   message,
	}

	logBuffer = append(logBuffer, entry)

	// Maintain circular buffer by trimming oldest entries
	if len(logBuffer) > maxLogEntries {
		logBuffer = logBuffer[len(logBuffer)-maxLogEntries:]
	}
}

// GetLogs retrieves all current log entries from the buffer with thread-safe
// access protection, returning a copy to prevent external modifications to
// the internal buffer. This function provides administrative interfaces with
// historical log data for debugging and monitoring purposes.
//
// Returns:
//   - []LogEntry: copy of all buffered log entries
func GetLogs() []LogEntry {
	logMutex.RLock()
	defer logMutex.RUnlock()

	result := make([]LogEntry, len(logBuffer))
	copy(result, logBuffer)
	return result
}

// ClearLogs removes all entries from the log buffer with thread-safe access
// protection, enabling administrative log management and memory recovery
// operations without requiring application restart or buffer reinitialization.
func ClearLogs() {
	logMutex.Lock()
	defer logMutex.Unlock()
	logBuffer = logBuffer[:0]
}

// colorizeLevel wraps the log level string with appropriate ANSI color codes
// for terminal output. Returns the colorized level string.
//
// Parameters:
//   - level: log level string (DEBUG, INFO, WARN, ERROR)
//
// Returns:
//   - string: colorized level string with ANSI codes
func colorizeLevel(level string) string {
	if color, ok := levelColors[level]; ok {
		return fmt.Sprintf("%s[%s]%s", color, level, colorReset)
	}
	return fmt.Sprintf("[%s]", level)
}

// logMessage formats a log message with printf-style arguments and outputs
// to both standard logging and the administrative log buffer simultaneously,
// ensuring visibility through both traditional logging mechanisms and web-based
// administrative interfaces without creating separate logging pathways.
//
// Parameters:
//   - level: severity level string for output formatting
//   - format: printf-style format string
//   - v: variadic arguments for format string interpolation
func logMessage(level string, format string, v ...interface{}) {
	message := fmt.Sprintf(format, v...)
	log.Printf("%s %s", colorizeLevel(level), message)
	addToBuffer(level, message)
}

// Debug logs debug-level messages for detailed troubleshooting and development
// purposes, filtering based on logger configuration to prevent verbose output
// in production environments while maintaining development visibility.
//
// Parameters:
//   - format: printf-style format string
//   - v: variadic arguments for format string interpolation
func (l *Logger) Debug(format string, v ...interface{}) {
	if l.shouldLog(DEBUG) {
		logMessage("DEBUG", format, v...)
	}
}

// Info logs informational messages about normal application operations and
// state changes, providing operational visibility while filtering unnecessary
// verbosity from production log streams.
//
// Parameters:
//   - format: printf-style format string
//   - v: variadic arguments for format string interpolation
func (l *Logger) Info(format string, v ...interface{}) {
	if l.shouldLog(INFO) {
		logMessage("INFO", format, v...)
	}
}

// Warn logs warning messages about potentially problematic conditions that
// don't prevent continued operation but warrant investigation or monitoring
// to prevent future issues or degraded performance.
//
// Parameters:
//   - format: printf-style format string
//   - v: variadic arguments for format string interpolation
func (l *Logger) Warn(format string, v ...interface{}) {
	if l.shouldLog(WARN) {
		logMessage("WARN", format, v...)
	}
}

// Error logs error messages about failures and exceptions requiring immediate
// attention or investigation, ensuring critical issues are visible regardless
// of log level configuration.
//
// Parameters:
//   - format: printf-style format string
//   - v: variadic arguments for format string interpolation
func (l *Logger) Error(format string, v ...interface{}) {
	if l.shouldLog(ERROR) {
		logMessage("ERROR", format, v...)
	}
}

// Package-level convenience functions for default logger access without
// requiring explicit logger instance management or initialization

// Debug logs debug-level messages using the default logger (package-level)
func Debug(format string, v ...interface{}) {
	getDefaultLogger().Debug(format, v...)
}

// Info logs info-level messages using the default logger (package-level)
func Info(format string, v ...interface{}) {
	getDefaultLogger().Info(format, v...)
}

// Warn logs warning-level messages using the default logger (package-level)
func Warn(format string, v ...interface{}) {
	getDefaultLogger().Warn(format, v...)
}

// Error logs error-level messages using the default logger (package-level)
func Error(format string, v ...interface{}) {
	getDefaultLogger().Error(format, v...)
}
