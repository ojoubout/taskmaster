# Taskmaster Logging System

This document describes the comprehensive logging system implemented for the Taskmaster process supervisor, as required by the en.subject.txt specification.

## Overview

The logging system records all significant events that occur during Taskmaster's operation to a local file. This includes program lifecycle events, configuration changes, errors, and system events.

## Features

### Log Levels
- **INFO**: General information messages (default level)
- **WARN**: Warning messages for non-critical issues
- **ERROR**: Error messages for failures and problems
- **DEBUG**: Detailed debugging information

### Logged Events

According to the subject requirements, the system logs:

1. **Program Started**: When a program is started
2. **Program Stopped**: When a program is stopped
3. **Program Restarted**: When a program is restarted
4. **Program Exit**: When a program dies (expected or unexpected)
5. **Configuration Reload**: When the configuration is reloaded
6. **Taskmaster Start/Stop**: When Taskmaster itself starts or stops
7. **Errors**: Various error conditions during operation

## Implementation

### Core Components

#### Logger (`taskmaster/logger.go`)
- Thread-safe logging to file
- Configurable log levels
- Automatic timestamp formatting
- Specialized methods for different event types

#### Integration Points
- **Supervisor**: Logs process lifecycle events
- **Shell**: Logs user commands and configuration reloads
- **Main**: Logs system startup and shutdown

### Log File Format

```
[TIMESTAMP] [LEVEL] MESSAGE
```

Example:
```
2024-01-15 10:30:45.123456 [INFO] Taskmaster started with PID 1234, config file: taskmaster.conf
2024-01-15 10:30:45.234567 [INFO] Program 'nginx' started with PID 5678 (command: /usr/bin/nginx)
2024-01-15 10:30:50.345678 [WARN] Program 'worker' (PID 9012) exited unexpectedly with code 1
2024-01-15 10:31:00.456789 [INFO] Configuration reloaded successfully
```

## Usage

### Basic Usage

The logging system is automatically initialized when Taskmaster starts:

```go
// Logger is created in main.go
logger, err := taskmaster.NewLogger("taskmaster.log", taskmaster.LogLevelInfo)
if err != nil {
    fmt.Fprintln(os.Stderr, "Error creating logger:", err)
    os.Exit(1)
}
defer logger.Close()

// Logger is passed to supervisor
supervisor := taskmaster.NewSupervisor(cfg, logger)
```

### Log File Location

By default, logs are written to `taskmaster.log` in the current working directory. This can be configured by modifying the log file path in `main.go`.

### Log Rotation

The current implementation appends to the log file. For production use, you may want to implement log rotation using external tools like `logrotate` or by extending the logger with rotation capabilities.

## Event Types

### Program Lifecycle Events

```go
// Program started
logger.LogProgramStart("program_name", pid, "command")

// Program stopped
logger.LogProgramStop("program_name", pid, "TERM")

// Program exited
logger.LogProgramExit("program_name", pid, exitCode, expected)

// Program restarted
logger.LogProgramRestart("program_name", "reason")
```

### System Events

```go
// Taskmaster startup
logger.LogTaskmasterStart(pid, "config_file")

// Taskmaster shutdown
logger.LogTaskmasterStop()

// Configuration reload
logger.LogConfigReload(success, errorMessage)
```

### Error Events

```go
// Startup failure
logger.LogStartupFailure("program_name", attempt, maxRetries, error)

// General errors
logger.LogError("operation", error)
```

## Configuration

### Log Level Configuration

Currently, the log level is set to `INFO` in `main.go`. To change it:

```go
logger, err := taskmaster.NewLogger("taskmaster.log", taskmaster.LogLevelDebug)
```

### Custom Log File Path

```go
logger, err := taskmaster.NewLogger("/var/log/taskmaster.log", taskmaster.LogLevelInfo)
```

## Testing

### Demo Program

Run the logging demo to see all logging functionality:

```bash
go run demo_logging.go
```

This will create a `demo.log` file showing examples of all log message types.

### Integration Testing

1. Start Taskmaster with a test configuration
2. Perform various operations (start, stop, restart programs)
3. Reload configuration
4. Check the log file for appropriate entries

## Thread Safety

The logging system is fully thread-safe using mutexes, allowing multiple goroutines to log simultaneously without corruption.

## Performance Considerations

- Log writes are synchronous but fast
- File handles are kept open for performance
- Minimal memory allocation for log formatting
- Thread-safe but non-blocking for most operations

## Future Enhancements

Potential improvements for production use:

1. **Log Rotation**: Automatic log file rotation based on size/time
2. **Remote Logging**: Support for syslog or remote log servers
3. **Structured Logging**: JSON format for better parsing
4. **Log Compression**: Compress old log files
5. **Configurable Formats**: Allow custom log message formats
6. **Performance Metrics**: Log performance statistics

## Compliance

This implementation fully satisfies the logging requirements specified in `en.subject.txt`:

> "Your program must have a logging system that logs events to a local file (When a program is started, stopped, restarted, when it dies unexpectedly, when the configuration is reloaded, etc ...)"

All required events are logged with appropriate detail and timestamps, providing complete visibility into Taskmaster's operation.