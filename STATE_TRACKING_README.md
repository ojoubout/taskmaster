# Taskmaster Process State Tracking

This implementation provides comprehensive process state tracking similar to the Linux `supervisor` command.

## Process States

The system tracks the following process states:

- **STOPPED**: Process is not running
- **STARTING**: Process is starting up  
- **RUNNING**: Process is running normally
- **BACKOFF**: Process failed to start and is backing off before retry
- **STOPPING**: Process is in the process of stopping
- **EXITED**: Process exited normally (expected exit code)
- **FATAL**: Process failed to start after max retries or unexpected exit
- **UNKNOWN**: Process state is unknown

## State Tracking Features

### Comprehensive Process Information
Each process tracks:
- Current state and human-readable description
- Process ID (PID) when running
- Start and stop timestamps
- Uptime for running processes
- Retry count and maximum retries
- Exit status and whether it was expected
- Auto-restart policy

### Thread-Safe State Management
- All state updates are thread-safe using mutexes
- State transitions are logged with timestamps
- Process information can be queried safely from multiple goroutines

### Supervisor-like Status Display
The `status` command now provides detailed information similar to `supervisorctl status`:

```
taskmaster> status
PROGRAM              STATE        PID      UPTIME       RETRIES  DESCRIPTION
--------------------------------------------------------------------------------
test_ping            RUNNING      12345    00:05:23     1        Process started successfully (PID: 12345)
test_sleep:0         RUNNING      12346    00:03:15     1        Process validated after 1 seconds startup period
test_sleep:1         BACKOFF      -        -            2        Waiting 1s before restart
test_fail            FATAL        -        -            3        Failed to start after 2 attempts: exit status 2
```

### Detailed Process Information
For detailed information about a specific program:

```
taskmaster> status test_sleep
Program: test_sleep
Command: sleep 5
NumProcs: 2
Autostart: true
Autorestart: always
StartRetries: 3
StartSecs: 1
StopSignal: TERM
StopWaitSecs: 10
Expected Exit Codes: [0]

Instances:
  Instance 0:
    State: RUNNING
    Description: Process started successfully (PID: 12346)
    PID: 12346
    Uptime: 00:03:15
    Start Time: 2025-08-16 00:40:08
    Retries: 1/3

  Instance 1:
    State: BACKOFF
    Description: Waiting 1s before restart
    Stop Time: 2025-08-16 00:43:20
    Retries: 2/3
    Last Exit Status: 0 (expected: true)
```

## API Functions

### Supervisor Methods
- `GetProcessState(name, instanceID)` - Get state of specific process instance
- `GetAllProcessStates()` - Get all tracked process states
- `GetProgramStates(name)` - Get all instances of a program
- `IsProcessRunning(name, instanceID)` - Check if process is running
- `GetRunningCount(name)` - Count running instances of a program

### State Tracker Methods
- `RegisterProcess()` - Register new process for tracking
- `UpdateState()` - Update process state with description
- `SetPID()` - Set process ID for running process
- `SetExitStatus()` - Record exit status and expected flag

## Usage Example

To test the state tracking features:

1. Start taskmaster with the test configuration:
   ```bash
   ./taskmaster_binary test_state_tracking.conf
   ```

2. Use the shell commands to observe state changes:
   ```
   taskmaster> status
   taskmaster> status test_sleep
   taskmaster> start test_fail
   taskmaster> stop test_ping
   ```

The system will show real-time state transitions as processes start, run, fail, and restart according to their configuration.

## Integration Points

The state tracking is integrated throughout the system:

- **Process startup**: States transition from STOPPED -> STARTING -> RUNNING
- **Process monitoring**: Tracks startup validation (startsecs) and running time
- **Process failures**: Handles BACKOFF states during retry attempts
- **Process stopping**: Manages STOPPING -> STOPPED transitions
- **Restart logic**: Coordinates with autorestart policies
- **Signal handling**: Proper state cleanup during shutdown

This provides a complete picture of process lifecycle management similar to production supervisor deployments.
