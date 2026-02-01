package logger

import (
	"fmt"
	"log"
	"strings"
	"sync"
)

type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
)

var (
	defaultLogger *Logger
	once          sync.Once
)

// Logger is a leveled logger instance
type Logger struct {
	level LogLevel
	mu    sync.RWMutex
}

// New creates a new Logger instance with the specified level
func New(level string) *Logger {
	return &Logger{
		level: ParseLogLevel(level),
	}
}

// getDefaultLogger returns the singleton default logger
func getDefaultLogger() *Logger {
	once.Do(func() {
		defaultLogger = &Logger{
			level: INFO,
		}
	})
	return defaultLogger
}

// ParseLogLevel converts string to LogLevel
func ParseLogLevel(level string) LogLevel {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return DEBUG
	case "INFO":
		return INFO
	case "WARN", "WARNING":
		return WARN
	case "ERROR":
		return ERROR
	default:
		return INFO
	}
}

// SetLogLevel sets the global default log level (package-level)
func SetLogLevel(level string) {
	getDefaultLogger().SetLevel(level)
}

// GetLogLevel returns current log level as string (package-level)
func GetLogLevel() string {
	return getDefaultLogger().GetLevel()
}

// SetLevel sets this logger instance's level
func (l *Logger) SetLevel(level string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = ParseLogLevel(level)
}

// GetLevel returns this logger instance's level as string
func (l *Logger) GetLevel() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	switch l.level {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	default:
		return "INFO"
	}
}

// shouldLog checks if message should be logged at current level
func (l *Logger) shouldLog(level LogLevel) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return level >= l.level
}

// logMessage formats and outputs the log message
func logMessage(level string, format string, v ...interface{}) {
	message := fmt.Sprintf(format, v...)
	log.Printf("[%s] %s", level, message)
}

// Instance methods (for use with struct fields like s.logger.Info())

// Debug logs debug level messages
func (l *Logger) Debug(format string, v ...interface{}) {
	if l.shouldLog(DEBUG) {
		logMessage("DEBUG", format, v...)
	}
}

// Info logs info level messages
func (l *Logger) Info(format string, v ...interface{}) {
	if l.shouldLog(INFO) {
		logMessage("INFO", format, v...)
	}
}

// Warn logs warning level messages
func (l *Logger) Warn(format string, v ...interface{}) {
	if l.shouldLog(WARN) {
		logMessage("WARN", format, v...)
	}
}

// Error logs error level messages
func (l *Logger) Error(format string, v ...interface{}) {
	if l.shouldLog(ERROR) {
		logMessage("ERROR", format, v...)
	}
}

// Package-level functions (for direct use like logger.Info())

// Debug logs debug level messages (package-level)
func Debug(format string, v ...interface{}) {
	getDefaultLogger().Debug(format, v...)
}

// Info logs info level messages (package-level)
func Info(format string, v ...interface{}) {
	getDefaultLogger().Info(format, v...)
}

// Warn logs warning level messages (package-level)
func Warn(format string, v ...interface{}) {
	getDefaultLogger().Warn(format, v...)
}

// Error logs error level messages (package-level)
func Error(format string, v ...interface{}) {
	getDefaultLogger().Error(format, v...)
}
