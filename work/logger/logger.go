package logger

import (
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// -- ANSI color codes for terminal output ------------------------------------
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
)

// LogLevel represents the severity level of log messages
type LogLevel int

const (
	DEBUG LogLevel = iota
	WARN
	ERROR
	INFO
	NONE
)

// LogEntry represents a single log message with associated metadata
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

var (
	defaultLogger *Logger
	once          sync.Once

	logBuffer     []LogEntry
	logMutex      sync.RWMutex
	maxLogEntries = 1000

	levelColors = map[string]string{
		"DEBUG": colorGreen,
		"WARN":  colorYellow,
		"ERROR": colorRed,
		"INFO":  colorBlue,
	}

	// -- patterns to obfuscate in log output --------------------------------
	sensitivePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(password|pass|passwd|pwd|secret|token|key)([=:\s"]+)([^\s"&,]+)`),
	}
)

// Logger provides leveled logging with thread-safe configuration
type Logger struct {
	level LogLevel
	mu    sync.RWMutex
}

// New creates a new Logger instance with the specified log level
func New(level string) *Logger {
	return &Logger{level: ParseLogLevel(level)}
}

// Init reads the DEBUG environment variable and configures the default logger
func Init() {
	l := getDefaultLogger()
	if strings.EqualFold(os.Getenv("DEBUG"), "true") {
		l.SetLevel("DEBUG")
	} else {
		l.SetLevel("INFO")
	}
	log.SetFlags(log.Ldate | log.Ltime)
}

// IsDebug returns true if the default logger is set to DEBUG level
func IsDebug() bool {
	return getDefaultLogger().level == DEBUG
}

// getDefaultLogger returns the singleton default logger instance
func getDefaultLogger() *Logger {
	once.Do(func() {
		defaultLogger = &Logger{level: INFO}
	})
	return defaultLogger
}

// ParseLogLevel converts string representations to LogLevel constants
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

// SetLogLevel updates the global default logger's minimum level
func SetLogLevel(level string) {
	getDefaultLogger().SetLevel(level)
}

// GetLogLevel retrieves the current global default logger's level as a string
func GetLogLevel() string {
	return getDefaultLogger().GetLevel()
}

// SetLevel updates this logger instance's minimum level
func (l *Logger) SetLevel(level string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = ParseLogLevel(level)
}

// GetLevel retrieves this logger instance's current level as a string
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

// shouldLog determines whether a message at the specified level should be logged
func (l *Logger) shouldLog(level LogLevel) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.level == NONE || level == NONE {
		return false
	}
	return level >= l.level
}

// addToBuffer appends a log entry to the circular buffer
func addToBuffer(level, message string) {
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
	if len(logBuffer) > maxLogEntries {
		logBuffer = logBuffer[len(logBuffer)-maxLogEntries:]
	}
}

// GetLogs retrieves all current log entries from the buffer
func GetLogs() []LogEntry {
	logMutex.RLock()
	defer logMutex.RUnlock()
	result := make([]LogEntry, len(logBuffer))
	copy(result, logBuffer)
	return result
}

// ClearLogs removes all entries from the log buffer
func ClearLogs() {
	logMutex.Lock()
	defer logMutex.Unlock()
	logBuffer = logBuffer[:0]
}

// colorizeLevel wraps the log level string with ANSI color codes
func colorizeLevel(level string) string {
	if color, ok := levelColors[level]; ok {
		return fmt.Sprintf("%s[%-5s]%s", color, level, colorReset)
	}
	return fmt.Sprintf("[%-5s]", level)
}

// obfuscate replaces sensitive values in a string with ***
func obfuscate(s string) string {
	for _, re := range sensitivePatterns {
		s = re.ReplaceAllStringFunc(s, func(match string) string {
			sub := re.FindStringSubmatch(match)
			if len(sub) < 4 {
				return match
			}
			return sub[1] + sub[2] + "***"
		})
	}
	return s
}

// logMessage formats and outputs a log message to stdout and the buffer
func logMessage(level string, format string, v ...interface{}) {
	message := fmt.Sprintf(format, v...)
	log.Printf("%s %s", colorizeLevel(level), message)
	addToBuffer(level, message)
}

// -- instance methods --------------------------------------------------------

// Debug logs debug-level messages
func (l *Logger) Debug(format string, v ...interface{}) {
	if l.shouldLog(DEBUG) {
		logMessage("DEBUG", format, v...)
	}
}

// Info logs informational messages
func (l *Logger) Info(format string, v ...interface{}) {
	if l.shouldLog(INFO) {
		logMessage("INFO", format, v...)
	}
}

// Warn logs warning messages
func (l *Logger) Warn(format string, v ...interface{}) {
	if l.shouldLog(WARN) {
		logMessage("WARN", format, v...)
	}
}

// Error logs error messages
func (l *Logger) Error(format string, v ...interface{}) {
	if l.shouldLog(ERROR) {
		logMessage("ERROR", format, v...)
	}
}

// -- package-level convenience functions -------------------------------------

// Debug logs debug-level messages using the default logger
func Debug(format string, v ...interface{}) { getDefaultLogger().Debug(format, v...) }

// Info logs info-level messages using the default logger
func Info(format string, v ...interface{}) { getDefaultLogger().Info(format, v...) }

// Warn logs warning-level messages using the default logger
func Warn(format string, v ...interface{}) { getDefaultLogger().Warn(format, v...) }

// Error logs error-level messages using the default logger
func Error(format string, v ...interface{}) { getDefaultLogger().Error(format, v...) }

// DebugSafe logs a debug message with sensitive values obfuscated
func DebugSafe(format string, v ...interface{}) {
	if !IsDebug() {
		return
	}
	safe := make([]interface{}, len(v))
	for i, a := range v {
		if s, ok := a.(string); ok {
			safe[i] = obfuscate(s)
		} else {
			safe[i] = a
		}
	}
	logMessage("DEBUG", obfuscate(format), safe...)
}

// InfoSafe logs an info message with sensitive values obfuscated
func InfoSafe(format string, v ...interface{}) {
	safe := make([]interface{}, len(v))
	for i, a := range v {
		if s, ok := a.(string); ok {
			safe[i] = obfuscate(s)
		} else {
			safe[i] = a
		}
	}
	logMessage("INFO", obfuscate(format), safe...)
}
