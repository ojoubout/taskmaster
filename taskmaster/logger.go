// Package taskmaster provides logging functionality for the supervisor
// This file implements a logging system that records events to a local file
package taskmaster

import (
	"fmt"
	"log"
	"os"
	"sync"
)

// LogLevel represents different levels of logging
type LogLevel int

const (
	LogLevelInfo LogLevel = iota
	LogLevelWarn
	LogLevelError
	LogLevelDebug
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

// Logger provides thread-safe logging to a file with different log levels
type Logger struct {
	file     *os.File
	logger   *log.Logger
	mu       sync.Mutex
	logLevel LogLevel
}

// NewLogger creates a new Logger instance that writes to the specified file
func NewLogger(logPath string, level LogLevel) (*Logger, error) {
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file %s: %v", logPath, err)
	}

	logger := log.New(file, "", log.LstdFlags|log.Lmicroseconds)

	return &Logger{
		file:     file,
		logger:   logger,
		logLevel: level,
	}, nil
}

// Close closes the log file handle
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// log is the internal logging method that handles the actual writing
func (l *Logger) log(level LogLevel, format string, args ...interface{}) {
	if level < l.logLevel {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	message := fmt.Sprintf(format, args...)
	logEntry := fmt.Sprintf("[%s] %s", level.String(), message)
	l.logger.Println(logEntry)
}

func (l *Logger) Info(format string, args ...interface{}) {
	l.log(LogLevelInfo, format, args...)
}

func (l *Logger) Warn(format string, args ...interface{}) {
	l.log(LogLevelWarn, format, args...)
}

func (l *Logger) Error(format string, args ...interface{}) {
	l.log(LogLevelError, format, args...)
}

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
