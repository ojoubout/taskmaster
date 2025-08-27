# Taskmaster Code Explanation

This document explains the taskmaster codebase in detail to help you understand and explain the project during evaluation.

## Project Overview

Taskmaster is a **process supervisor** (similar to `supervisord`) written in Go. It manages child processes, monitors their health, and provides an interactive control interface. Think of it as a daemon that keeps your programs running and provides tools to control them.

## Architecture Overview

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   main.go       │───▶│   Supervisor     │───▶│  Child Process  │
│ (Entry Point)   │    │ (Core Manager)   │    │  (Your Program) │
└─────────────────┘    └──────────────────┘    └─────────────────┘
         │                       │                       │
         │              ┌──────────────────┐             │
         └─────────────▶│  Control Shell   │             │
                        │ (User Interface) │             │
                        └──────────────────┘             │
                                 │                       │
                        ┌──────────────────┐             │
                        │ Signal Handler   │             │
                        │ (Config Reload)  │             │
                        └──────────────────┘             │
                                                         │
                        ┌──────────────────┐             │
                        │ Config Parser    │             │
                        │ (YAML Reader)    │             │
                        └──────────────────┘             │
```

## File-by-File Explanation

### 1. `main.go` - Application Entry Point

**Purpose**: This is where the program starts. It coordinates all components.

**Key Concepts**:
- **Configuration Loading**: Reads `taskmaster.conf` (YAML format)
- **Component Integration**: Creates supervisor, shell, and signal handlers
- **Process Display**: Shows what programs were loaded

**Flow**:
1. Load configuration from YAML file
2. Create supervisor (the main manager)
3. Start programs marked with `autostart: true`
4. Set up signal handling (for config reload)
5. Start interactive shell (command interface)

### 2. `taskmaster/parsing.go` - Configuration Management

**Purpose**: Handles reading and validating YAML configuration files.

**Key Concepts**:
- **YAML Parsing**: Converts YAML text into Go structs
- **Custom Types**: `ExitCodes` can handle both single values and arrays
- **Validation**: Ensures all required fields are present and valid

**Important Structs**:
```go
type Program struct {
    Command      string            // What to execute: "/bin/sleep 10"
    NumProcs     int               // How many instances: 3
    Autostart    bool              // Start automatically: true
    Autorestart  string            // When to restart: "always"/"never"/"unexpected"
    ExitCodes    ExitCodes         // Success codes: [0] or [0,1,2]
    Directory    string            // Working directory: "/tmp"
    Stdout       string            // Redirect stdout: "/var/log/app.log"
    // ... more fields
}
```

**Why Custom ExitCodes Type?**:
YAML allows both `exitcodes: 0` and `exitcodes: [0,1,2]`. The custom type handles both formats.

### 3. `taskmaster/execution.go` - Process Management Core

**Purpose**: The heart of the system - starts, monitors, and stops processes.

**Key Concepts**:
- **Process Lifecycle**: Start → Monitor → Stop/Restart
- **Goroutines**: Each process runs in its own lightweight thread
- **Context**: Used for coordinated shutdown across all goroutines
- **File Redirection**: Captures program output to files

**Main Functions**:

#### `startSingleWorkerNew()` - Process Creation
```go
// This function does the heavy lifting:
1. Parse command string: "/bin/sleep 10" → ["/bin/sleep", "10"]
2. Set working directory and environment variables
3. Set up stdout/stderr file redirection
4. Start the actual process (cmd.Start())
5. Launch monitoring goroutine
6. Return PID and command handle for tracking
```

#### `Supervisor.StartProgram()` - Multi-Instance Management
```go
// For programs with numprocs > 1:
for i := 0; i < programConfig.NumProcs; i++ {
    // Start instance 0, 1, 2, etc.
    // Store each in programs[program_name][instance_id]
}
```

#### `StopProcess()` - Graceful Shutdown
```go
// Shutdown process:
1. Send configured signal (TERM, INT, KILL, etc.)
2. Wait up to stopwaitsecs for graceful exit
3. If timeout, send SIGKILL (force kill)
4. Clean up resources
```

**Threading Model**:
- **Main goroutine**: Runs the shell interface
- **Monitor goroutines**: One per process, watches for exit
- **Signal goroutine**: Handles SIGHUP/SIGINT
- **Context cancellation**: Coordinates shutdown

### 4. `taskmaster/shell.go` - Interactive Control Interface

**Purpose**: Provides a command-line interface like `supervisorctl`.

**Key Concepts**:
- **Command Parsing**: Splits user input into command + arguments
- **Thread Safety**: Uses mutex to safely access shared data
- **Status Display**: Shows PID, status, and program info

**Available Commands**:
```bash
status           # Show all programs
status program   # Show specific program details
start program    # Start a program
stop program     # Stop a program  
restart program  # Stop then start
reload          # Reload configuration
quit            # Shutdown taskmaster
```

**Status Display Logic**:
```go
// For each process, check if it's actually running:
err := proc.Process.Signal(syscall.Signal(0))
if err == nil {
    status = "RUNNING"  // Process exists
} else {
    status = "STOPPED"  // Process not found
}
```

**Configuration Reload**:
The shell can reload config without restarting taskmaster:
1. Load new configuration file
2. Compare old vs new programs
3. Stop removed programs
4. Start new programs
5. Restart changed programs

### 5. `taskmaster/signals.go` - System Signal Handling

**Purpose**: Handles Unix signals for daemon-like behavior.

**Key Signals**:
- **SIGHUP**: Reload configuration (common daemon pattern)
- **SIGINT**: Graceful shutdown (Ctrl+C)

**How It Works**:
```go
// Create signal channel
sigs := make(chan os.Signal, 1)
signal.Notify(sigs, syscall.SIGHUP, syscall.SIGINT)

// Handle signals in background goroutine
go func() {
    for sig := range sigs {
        switch sig {
        case syscall.SIGHUP: reload_config()
        case syscall.SIGINT:  shutdown_gracefully()
        }
    }
}()
```

## Go Language Concepts Explained

### 1. Goroutines (Lightweight Threads)
```go
go function_name()  // Runs function concurrently
```
- Much lighter than OS threads
- Managed by Go runtime
- Perfect for I/O bound tasks like process monitoring

### 2. Channels (Thread Communication)
```go
done := make(chan error, 1)  // Create channel
done <- err                  // Send to channel
result := <-done             // Receive from channel
```
- Safe way to communicate between goroutines
- Can be buffered or unbuffered

### 3. Context (Cancellation)
```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()  // Clean up when function returns
```
- Coordinates cancellation across goroutines
- When `cancel()` is called, all goroutines can detect it

### 4. Mutex (Thread Safety)
```go
mu.Lock()        // Exclusive access
defer mu.Unlock()  // Always unlock when function returns
```
- Prevents race conditions when multiple goroutines access shared data

### 5. Select Statement (Channel Operations)
```go
select {
case <-timeout:     // Timeout expired
case result := <-done:  // Operation completed
}
```
- Waits for first available channel operation
- Perfect for timeouts and cancellation

## Configuration File Format

```yaml
programs:
    my_program:
        command: "/bin/sleep 30"
        numprocs: 2                    # Run 2 instances
        directory: /tmp                # Working directory
        autostart: true                # Start when taskmaster starts
        autorestart: always            # always/never/unexpected
        exitcodes: [0, 1]             # Success exit codes
        startretries: 3               # Retry 3 times if start fails
        startsecs: 2                  # Must run 2 seconds to be "started"
        stopsignal: TERM              # Signal to send when stopping
        stopwaitsecs: 5               # Wait 5 seconds before SIGKILL
        stdout: /tmp/my_program.out   # Redirect stdout
        stderr: /tmp/my_program.err   # Redirect stderr
        env:                          # Environment variables
            DEBUG: "1"
            PATH: "/usr/bin"
```

## Common Evaluation Questions & Answers

### Q: How does process monitoring work?
**A**: Each process runs in its own goroutine that calls `cmd.Wait()`. This blocks until the process exits. We can also check if a process is alive using `proc.Process.Signal(syscall.Signal(0))` - signal 0 doesn't actually send a signal, just checks if the process exists.

### Q: How is thread safety handled?
**A**: We use `sync.RWMutex` to protect the `programs` map. Multiple goroutines can read simultaneously (`RLock()`) but only one can write (`Lock()`). This prevents race conditions.

### Q: What happens when a process exits?
**A**: The monitoring goroutine detects the exit via `cmd.Wait()`. It then checks the exit code against the configured `exitcodes` and the `autorestart` policy to decide whether to restart.

### Q: How does the shell communicate with the supervisor?
**A**: The shell holds a reference to the supervisor struct and calls its methods directly (like `StartProgram()`, `StopProcess()`). Since they run in the same process, this is safe with proper locking.

### Q: How does configuration reload work?
**A**: The signal handler loads a new config and compares it with the old one. It stops processes that were removed, starts new ones, and restarts modified ones. This allows configuration changes without restarting taskmaster.

## Key Design Decisions

1. **Goroutines over threads**: Lighter weight, easier to manage
2. **Context for cancellation**: Clean way to coordinate shutdown
3. **Mutex for thread safety**: Prevents race conditions
4. **Channels for communication**: Safe inter-goroutine communication
5. **YAML for configuration**: Human-readable, supports complex data types
6. **Interactive shell**: Familiar interface for users (like supervisorctl)

This architecture provides a robust, concurrent process supervisor that can handle multiple programs with proper lifecycle management, monitoring, and control interfaces.
