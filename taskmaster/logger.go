// Package taskmaster provides logging functionality for the supervisor
// This file implements a logging system that records events to a local file
package taskmaster

import (
	"fmt"      // For formatted string operations
	"log"      // For standard logging functionality
	"os"       // For file operations
	"sync"     // For synchronization primitives (mutex for thread safety)
)

// LogLevel represents different levels of logging
type LogLevel int

const (
	LogLevelInfo LogLevel = iota  // General information messages
	LogLevelWarn                  // Warning messages
	LogLevelError                 // Error messages
	LogLevelDebug                 // Debug messages (detailed information)
)

// String returns the string representation of a log level
func (l LogLevel) String() string {
	switch l {
	case LogLevelInfo:
		return "INFO"
	case LogLevelWarn:
		return "WARN"
	case LogLevelError:
		return "ERROR"
	case LogLevelDebug:
		return "DEBUG"
	default:
		return "UNKNOWN"
	}
}

// Logger represents the logging system for taskmaster
// It provides thread-safe logging to a file with different log levels
type Logger struct {
	file     *os.File     // File handle for the log file
	logger   *log.Logger  // Standard library logger for formatting
	mu       sync.Mutex   // Mutex for thread-safe writing
	logLevel LogLevel     // Minimum log level to write
}

// NewLogger creates a new Logger instance that writes to the specified file
// If the file doesn't exist, it will be created. If it exists, logs will be appended.
func NewLogger(logPath string, level LogLevel) (*Logger, error) {
	// Open or create the log file with append mode
	// 0644 means readable by owner/group/others, writable by owner only
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file %s: %v", logPath, err)
	}

	// Create a standard library logger that writes to our file
	// The flags control the format: date, time, and microseconds
	logger := log.New(file, "", log.LstdFlags|log.Lmicroseconds)

	return &Logger{
		file:     file,
		logger:   logger,
		logLevel: level,
	}, nil
}

// Close closes the log file handle
// This should be called when the logger is no longer needed
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// log is the internal logging method that handles the actual writing
// It checks the log level and formats the message appropriately
func (l *Logger) log(level LogLevel, format string, args ...interface{}) {
	// Only log if the message level is at or above our configured level
	if level < l.logLevel {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Format the message with the provided arguments
	message := fmt.Sprintf(format, args...)
	
	// Create the full log entry with level prefix
	logEntry := fmt.Sprintf("[%s] %s", level.String(), message)
	
	// Write to the log file using the standard logger
	l.logger.Println(logEntry)
}

// Info logs an informational message
func (l *Logger) Info(format string, args ...interface{}) {
	l.log(LogLevelInfo, format, args...)
}

// Warn logs a warning message
func (l *Logger) Warn(format string, args ...interface{}) {
	l.log(LogLevelWarn, format, args...)
}

// Error logs an error message
func (l *Logger) Error(format string, args ...interface{}) {
	l.log(LogLevelError, format, args...)
}

// Debug logs a debug message
func (l *Logger) Debug(format string, args ...interface{}) {
	l.log(LogLevelDebug, format, args...)
}

// LogProgramStart logs when a program is started
func (l *Logger) LogProgramStart(programName string, pid int, command string) {
	l.Info("Program '%s' started with PID %d (command: %s)", programName, pid, command)
}

// LogProgramStop logs when a program is stopped
func (l *Logger) LogProgramStop(programName string, pid int, signal string) {
	l.Info("Program '%s' (PID %d) stopped with signal %s", programName, pid, signal)
}

// LogProgramRestart logs when a program is restarted
func (l *Logger) LogProgramRestart(programName string, reason string) {
	l.Info("Program '%s' restarted (%s)", programName, reason)
}

// LogProgramExit logs when a program exits (expected or unexpected)
func (l *Logger) LogProgramExit(programName string, pid int, exitCode int, expected bool) {
	if expected {
		l.Info("Program '%s' (PID %d) exited with expected code %d", programName, pid, exitCode)
	} else {
		l.Warn("Program '%s' (PID %d) exited unexpectedly with code %d", programName, pid, exitCode)
	}
}

// LogConfigReload logs when the configuration is reloaded
func (l *Logger) LogConfigReload(success bool, error string) {
	if success {
		l.Info("Configuration reloaded successfully")
	} else {
		l.Error("Configuration reload failed: %s", error)
	}
}

// LogTaskmasterStart logs when taskmaster itself starts
func (l *Logger) LogTaskmasterStart(pid int, configFile string) {
	l.Info("Taskmaster started with PID %d, config file: %s", pid, configFile)
}

// LogTaskmasterStop logs when taskmaster is shutting down
func (l *Logger) LogTaskmasterStop() {
	l.Info("Taskmaster shutting down")
}

// LogError logs general errors that occur during operation
func (l *Logger) LogError(operation string, err error) {
	l.Error("Error during %s: %v", operation, err)
}

// LogStartupFailure logs when a program fails to start
func (l *Logger) LogStartupFailure(programName string, attempt int, maxRetries int, err error) {
	l.Error("Program '%s' failed to start (attempt %d/%d): %v", programName, attempt, maxRetries, err)
}

// LogProcessMonitoring logs process monitoring events
func (l *Logger) LogProcessMonitoring(programName string, pid int, event string) {
	l.Debug("Process monitoring for '%s' (PID %d): %s", programName, pid, event)
}