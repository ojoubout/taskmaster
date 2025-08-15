// Package taskmaster provides process supervision and execution management
// This file contains the core supervisor logic and process lifecycle management
package taskmaster

import (
	"context"   // For handling cancellation and timeouts across goroutines
	"fmt"       // For formatted I/O operations (printing status messages)
	"os"        // For file operations and process management
	"os/exec"   // For executing external programs
	"strings"   // For string manipulation (splitting command arguments)
	"sync"      // For synchronization primitives (WaitGroup, mutex)
	"syscall"   // For system calls (signals, umask)
	"time"      // For time-based operations (delays, timeouts)
)

// Supervisor is the main component that manages all child processes
// It tracks running programs, handles their lifecycle, and provides control interface
type Supervisor struct {
	cfg         *Config                     // Configuration loaded from YAML file
	programs    map[string]map[int]*exec.Cmd // Map: program_name -> instance_id -> process
	retryCount  map[string]map[int]int      // Map: program_name -> instance_id -> retry_attempts
	ctx         context.Context             // Context for cancellation across all goroutines
	cancel      context.CancelFunc          // Function to cancel the context (stops all goroutines)
	wg          sync.WaitGroup              // Wait group to track running goroutines
	mu          sync.RWMutex                // Read-Write mutex for thread-safe access to programs map
	logger      *Logger                     // Logger for recording events
}

// NewSupervisor creates a new Supervisor instance with the given configuration
// It initializes the context for cancellation and the programs tracking map
func NewSupervisor(cfg *Config, logger *Logger) *Supervisor {
	ctx, cancel := context.WithCancel(context.Background())
	return &Supervisor{
		cfg:        cfg,
		programs:   make(map[string]map[int]*exec.Cmd), // Initialize empty programs map
		retryCount: make(map[string]map[int]int),       // Initialize retry counter map
		ctx:        ctx,
		cancel:     cancel,
		logger:     logger,
	}
}

// startSingleWorker creates and starts a single process instance
// This is the core function that actually executes programs and sets up monitoring
// Returns: PID of started process, the *exec.Cmd for management, and program name
func startSingleWorker(programName string, programConfig *Program, instanceID int, spv *Supervisor) (int, *exec.Cmd) {
	// Step 1: Parse command string into program and arguments
	// Example: "/bin/sleep 10" becomes ["/bin/sleep", "10"]
	parts := strings.Fields(programConfig.Command)
	cmd := exec.CommandContext(spv.ctx, parts[0], parts[1:]...)

	// Step 2: Set working directory if specified
	if programConfig.Directory != "" {
		cmd.Dir = programConfig.Directory
	}

	// Step 3: Set up environment variables
	// Start with system environment and add custom variables from config
	env := os.Environ()  // Get current environment
	for k, v := range programConfig.Env {
		env = append(env, k+"="+v)  // Add each custom env var
	}
	cmd.Env = env

	// Step 4: Set up stdout redirection if specified
	if programConfig.Stdout != "" {
		// Open file with create, write-only, append flags
		// 0644 means readable by owner/group/others, writable by owner only
		stdoutFile, err := os.OpenFile(programConfig.Stdout, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			fmt.Printf("Failed to open stdout file %s: %v\n", programConfig.Stdout, err)
		} else {
			cmd.Stdout = stdoutFile  // Redirect process stdout to file
		}
	}

	// Step 5: Set up stderr redirection (same pattern as stdout)
	if programConfig.Stderr != "" {
		stderrFile, err := os.OpenFile(programConfig.Stderr, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			fmt.Printf("Failed to open stderr file %s: %v\n", programConfig.Stderr, err)
		} else {
			cmd.Stderr = stderrFile  // Redirect process stderr to file
		}
	}

	// Step 6: Set umask (file permission mask) if specified
	var oldUmask int
	if programConfig.Umask != 0 {
		oldUmask = syscall.Umask(programConfig.Umask)  // Set new umask, save old one
		defer syscall.Umask(oldUmask)                  // Restore old umask when function returns
	}

	fmt.Printf("[taskmaster] Starting program: %s\n", programConfig.Command)
	
	// Step 7: Actually start the process with retry logic
	maxStartRetries := programConfig.StartRetries
	if maxStartRetries <= 0 {
		maxStartRetries = 3 // Default retry count if not specified
	}
	
	var err error
	var startAttempt int
	
	for startAttempt = 1; startAttempt <= maxStartRetries; startAttempt++ {
		// Create a fresh command for each attempt
		if startAttempt > 1 {
			parts := strings.Fields(programConfig.Command)
			cmd = exec.CommandContext(spv.ctx, parts[0], parts[1:]...)
			
			// Re-apply all the configuration for retry attempts
			if programConfig.Directory != "" {
				cmd.Dir = programConfig.Directory
			}
			
			env := os.Environ()
			for k, v := range programConfig.Env {
				env = append(env, k+"="+v)
			}
			cmd.Env = env
			
			if programConfig.Stdout != "" {
				stdoutFile, err := os.OpenFile(programConfig.Stdout, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
				if err != nil {
					fmt.Printf("Failed to open stdout file %s: %v\n", programConfig.Stdout, err)
				} else {
					cmd.Stdout = stdoutFile
				}
			}
			
			if programConfig.Stderr != "" {
				stderrFile, err := os.OpenFile(programConfig.Stderr, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
				if err != nil {
					fmt.Printf("Failed to open stderr file %s: %v\n", programConfig.Stderr, err)
				} else {
					cmd.Stderr = stderrFile
				}
			}
			
			if programConfig.Umask != 0 {
				oldUmask = syscall.Umask(programConfig.Umask)
				defer syscall.Umask(oldUmask)
			}
		}
		
		err = cmd.Start()
		if err == nil {
			// Success! Break out of retry loop
			break
		}
		
		// Log the failed attempt
		fmt.Printf("[taskmaster] Failed to start %s (attempt %d/%d): %v\n", 
			programConfig.Command, startAttempt, maxStartRetries, err)
		if spv.logger != nil {
			spv.logger.LogStartupFailure(programName, startAttempt, maxStartRetries, err)
		}
		
		// If this wasn't the last attempt, wait before retrying
		if startAttempt < maxStartRetries {
			retryDelay := 1 * time.Second
			fmt.Printf("[taskmaster] Retrying startup in %v...\n", retryDelay)
			
			select {
			case <-spv.ctx.Done():
				// Supervisor is shutting down, don't retry
				return 0, nil
			case <-time.After(retryDelay):
				// Delay completed, continue to next attempt
			}
		}
	}
	
	// Check if all attempts failed
	if err != nil {
		fmt.Printf("[taskmaster] Failed to start %s after %d attempts, giving up\n", 
			programConfig.Command, maxStartRetries)
		return 0, nil  // Return 0 PID and nil cmd to indicate failure
	}

	// Step 8: Get the PID and log success
	pid := cmd.Process.Pid
	fmt.Printf("[taskmaster] Started %s with PID %d\n", programConfig.Command, pid)
	if spv.logger != nil {
		spv.logger.LogProgramStart(programName, pid, programConfig.Command)
	}

	// Step 9: Define cleanup function for file handles
	// This ensures files are properly closed when process ends
	closeFiles := func() {
		if cmd.Stdout != nil {
			if file, ok := cmd.Stdout.(*os.File); ok {  // Type assertion to check if it's a file
				file.Close()
			}
		}
		if cmd.Stderr != nil {
			if file, ok := cmd.Stderr.(*os.File); ok {
				file.Close()
			}
		}
	}

	// Step 10: Start monitoring goroutine
	// This runs concurrently to monitor the process lifecycle
	spv.wg.Add(1)  // Add to wait group so we can wait for all goroutines to finish
	go func() {
		defer spv.wg.Done()  // Mark this goroutine as done when it exits
		defer closeFiles()   // Ensure files are closed when goroutine exits

		// Step 10a: Handle startsecs validation
		// The process must run for at least startsecs to be considered "successfully started"
		if programConfig.StartSecs > 0 {
			timer := time.NewTimer(time.Duration(programConfig.StartSecs) * time.Second)
			defer timer.Stop()  // Clean up timer
			
			select {
			case <-spv.ctx.Done():  // If supervisor is shutting down, exit
				return
			case <-timer.C:  // Timer expired - process survived startsecs
				fmt.Printf("[taskmaster] Process %d survived startsecs, now monitoring\n", pid)
			}
		}

		// Step 10b: Wait for process to exit
		// cmd.Wait() blocks until the process terminates
		err = cmd.Wait()
		
		// Determine exit code and whether it was expected
		exitCode := 0
		expected := true
		if err != nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				exitCode = exitError.ExitCode()
			}
			// Check if this exit code is in the expected list
			expected = false
			for _, expectedCode := range programConfig.ExitCodes {
				if exitCode == expectedCode {
					expected = true
					break
				}
			}
			fmt.Printf("[taskmaster] Process %d exited with error: %v\n", pid, err)
		} else {
			fmt.Printf("[taskmaster] Process %d exited successfully\n", pid)
		}
		
		// Log the exit event
		if spv.logger != nil {
			spv.logger.LogProgramExit(programName, pid, exitCode, expected)
		}

		// Step 10c: Handle restart logic based on autorestart policy
		spv.handleProcessRestart(programName, programConfig, instanceID, expected, exitCode)
	}()

	return pid, cmd  // Return PID and command for tracking
}

// handleProcessRestart determines if and how to restart a process based on autorestart policy
// This implements the core autorestart functionality according to supervisor behavior
func (spv *Supervisor) handleProcessRestart(programName string, programConfig *Program, instanceID int, expectedExit bool, exitCode int) {
	// Determine if we should restart based on autorestart policy
	shouldRestart := false
	
	switch programConfig.Autorestart {
	case "always":
		// Always restart regardless of exit status
		shouldRestart = true
		fmt.Printf("[taskmaster] Program %s (instance %d) will be restarted (policy: always)\n", programName, instanceID)
	case "never":
		// Never restart
		shouldRestart = false
		fmt.Printf("[taskmaster] Program %s (instance %d) will not be restarted (policy: never)\n", programName, instanceID)
	case "unexpected", "":
		// Restart only on unexpected exits (default behavior if empty)
		shouldRestart = !expectedExit
		if shouldRestart {
			fmt.Printf("[taskmaster] Program %s (instance %d) will be restarted (unexpected exit with code %d)\n", programName, instanceID, exitCode)
		} else {
			fmt.Printf("[taskmaster] Program %s (instance %d) will not be restarted (expected exit with code %d)\n", programName, instanceID, exitCode)
		}
	default:
		// Invalid autorestart value, treat as "never"
		shouldRestart = false
		fmt.Printf("[taskmaster] Program %s (instance %d) has invalid autorestart policy '%s', treating as 'never'\n", programName, instanceID, programConfig.Autorestart)
	}
	
	if !shouldRestart {
		// Remove the process from tracking since it won't be restarted
		spv.mu.Lock()
		if spv.programs[programName] != nil {
			delete(spv.programs[programName], instanceID)
			// If no more instances, remove the program entirely
			if len(spv.programs[programName]) == 0 {
				delete(spv.programs, programName)
			}
		}
		spv.mu.Unlock()
		return
	}
	
	// Initialize retry tracking if needed
	spv.mu.Lock()
	if spv.retryCount[programName] == nil {
		spv.retryCount[programName] = make(map[int]int)
	}
	spv.mu.Unlock()
	
	// Runtime restarts are unlimited in supervisor - no retry limit
	// Only controlled by autorestart policy and backoff delay
	
	// Add a small delay before restarting to prevent rapid restart loops
	restartDelay := 1 * time.Second
	fmt.Printf("[taskmaster] Restarting program %s (instance %d) in %v\n", 
		programName, instanceID, restartDelay)
	
	// Log the restart attempt
	if spv.logger != nil {
		spv.logger.LogProgramRestart(programName, "autorestart")
	}
	
	// Wait before restarting
	select {
	case <-spv.ctx.Done():
		// Supervisor is shutting down, don't restart
		return
	case <-time.After(restartDelay):
		// Delay completed, proceed with restart
	}
	
	// Start the replacement process
	pid, newCmd := startSingleWorker(programName, programConfig, instanceID, spv)
	if newCmd != nil {
		// Update the tracking map with the new process
		spv.mu.Lock()
		if spv.programs[programName] == nil {
			spv.programs[programName] = make(map[int]*exec.Cmd)
		}
		spv.programs[programName][instanceID] = newCmd
		spv.mu.Unlock()
		
		fmt.Printf("[taskmaster] Successfully restarted program %s (instance %d) with new PID %d\n", programName, instanceID, pid)
	} else {
		fmt.Printf("[taskmaster] Failed to restart program %s (instance %d)\n", programName, instanceID)
		// The retry will be handled by the new process's monitoring goroutine if it fails again
	}
}

// StartProgram starts all instances of a program according to its numprocs setting
// This is called by the shell when user types "start program_name"
func (spv *Supervisor) StartProgram(programName string, programConfig *Program) {
	// Initialize the program's process map if it doesn't exist
	if spv.programs[programName] == nil {
		spv.programs[programName] = make(map[int]*exec.Cmd)
	}
	
	// Start the specified number of processes (numprocs)
	// Each process gets an instance ID: 0, 1, 2, etc.
	for i := 0; i < programConfig.NumProcs; i++ {
		pid, cmd := startSingleWorker(programName, programConfig, i, spv)
		if cmd != nil {
			// Store the command in our tracking map
			// Key structure: programs[program_name][instance_id] = *exec.Cmd
			spv.programs[programName][i] = cmd
			fmt.Printf("[taskmaster] Stored process %d for program %s (instance %d)\n", pid, programName, i)
		}
	}
}

// RunInitialState starts all programs that have autostart=true
// This is called when taskmaster first starts up
func RunInitialState(spv *Supervisor) {
	spv.mu.Lock()    // Lock for writing since we're modifying the programs map
	defer spv.mu.Unlock()
	
	// Iterate through all configured programs
	for name, programConfig := range spv.cfg.Programs {
		if !programConfig.Autostart {
			continue  // Skip programs that shouldn't auto-start
		}
		// Start programs with autostart=true
		spv.StartProgram(name, &programConfig)
	}
}

// StopProgram stops all instances of a program
// This is called by the shell when user types "stop program_name"
func (spv *Supervisor) StopProgram(programName string, programConfig *Program) error {
	processes, running := spv.programs[programName]
	if !running || len(processes) == 0 {
		return fmt.Errorf("program '%s' is not running", programName)
	}
	
	// Stop each running process
	for _, proc := range processes {
		if proc != nil {
			err := spv.StopProcess(proc, programName, programConfig)
			if err != nil {
				if spv.logger != nil {
					spv.logger.LogError("stopping program "+programName, err)
				}
			}
		}
	}
	
	// Remove from running programs map
	delete(spv.programs, programName)
	return nil
}

// StopProcess gracefully stops a process using the configured stop signal
// If the process doesn't exit within stopwaitsecs, it sends SIGKILL
func (spv *Supervisor) StopProcess(cmd *exec.Cmd, programName string, programConfig *Program) error {
	// Validate that we have a running process to stop
	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("process not running")
	}

	// Step 1: Parse the stop signal from string to syscall.Signal
	// Default to SIGTERM if not specified or invalid
	sig := syscall.SIGTERM // Default signal
	switch programConfig.StopSignal {
	case "HUP":
		sig = syscall.SIGHUP   // Hangup signal
	case "INT":
		sig = syscall.SIGINT   // Interrupt signal (Ctrl+C)
	case "QUIT":
		sig = syscall.SIGQUIT  // Quit signal (Ctrl+\)
	case "KILL":
		sig = syscall.SIGKILL  // Kill signal (cannot be caught)
	case "USR1":
		sig = syscall.SIGUSR1  // User-defined signal 1
	case "USR2":
		sig = syscall.SIGUSR2  // User-defined signal 2
	case "TERM":
		sig = syscall.SIGTERM  // Termination signal (default)
	}

	// Step 2: Send the stop signal to the process
	pid := cmd.Process.Pid
	signalName := programConfig.StopSignal
	if signalName == "" {
		signalName = "TERM"
	}
	
	fmt.Printf("[taskmaster] Sending signal %s to process %d\n", signalName, pid)
	if spv.logger != nil {
		spv.logger.LogProgramStop(programName, pid, signalName)
	}
	
	err := cmd.Process.Signal(sig)
	if err != nil {
		return fmt.Errorf("failed to send signal: %v", err)
	}

	// Step 3: Wait for process to exit gracefully, with timeout
	waitSecs := programConfig.StopWaitSecs
	if waitSecs <= 0 {
		waitSecs = 5 // Default wait time if not specified
	}
	
	// Use a goroutine to wait for process exit
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()  // This blocks until process exits
	}()

	// Step 4: Race between timeout and process exit
	select {
	case <-time.After(time.Duration(waitSecs) * time.Second):
		// Timeout expired - process didn't exit gracefully, force kill it
		fmt.Printf("[taskmaster] Process %d did not exit after %d seconds, killing\n", cmd.Process.Pid, waitSecs)
		err := cmd.Process.Kill()  // Send SIGKILL (cannot be ignored)
		if err != nil {
			return fmt.Errorf("failed to kill process: %v", err)
		}
		<-done // Wait for the Wait() call to complete
		return nil
	case err := <-done:
		// Process exited gracefully before timeout
		fmt.Printf("[taskmaster] Process %d exited after signal\n", cmd.Process.Pid)
		return err
	}
}
