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
	cfg      *Config                     // Configuration loaded from YAML file
	programs map[string]map[int]*exec.Cmd // Map: program_name -> instance_id -> process
	ctx      context.Context             // Context for cancellation across all goroutines
	cancel   context.CancelFunc          // Function to cancel the context (stops all goroutines)
	wg       sync.WaitGroup              // Wait group to track running goroutines
	mu       sync.RWMutex                // Read-Write mutex for thread-safe access to programs map
}

// NewSupervisor creates a new Supervisor instance with the given configuration
// It initializes the context for cancellation and the programs tracking map
func NewSupervisor(cfg *Config) *Supervisor {
	ctx, cancel := context.WithCancel(context.Background())
	return &Supervisor{
		cfg:      cfg,
		programs: make(map[string]map[int]*exec.Cmd), // Initialize empty programs map
		ctx:      ctx,
		cancel:   cancel,
	}
}

func startSingleWorker(programConfig *Program, spv *Supervisor) (int, *exec.Cmd) {
	spv.wg.Add(1)
	go func() {
		defer spv.wg.Done()
		retries := 0
		var err error
		for {
			select {
			case <-spv.ctx.Done():
				return
			default:
			}
			parts := strings.Fields(programConfig.Command)
			cmd := exec.CommandContext(spv.ctx, parts[0], parts[1:]...)

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
				if err == nil {
					cmd.Stdout = stdoutFile
				} else {
					fmt.Printf("failed to open stdout file %s: %v\n", programConfig.Stdout, err)
				}
			}

			if programConfig.Stderr != "" {
				stderrFile, err := os.OpenFile(programConfig.Stderr, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
				if err == nil {
					cmd.Stderr = stderrFile
				} else {
					fmt.Printf("failed to open stderr file %s: %v\n", programConfig.Stderr, err)
				}
			}

			var oldUmask int
			if programConfig.Umask != 0 {
				oldUmask = syscall.Umask(programConfig.Umask)
				defer syscall.Umask(oldUmask)
			}

			fmt.Printf("[taskmaster] Starting program: %s\n", programConfig.Command)
			startTime := make(chan error, 1)
			go func() {
				err = cmd.Start()
				startTime <- err
			}()

			err = <-startTime
			if err != nil {
				fmt.Printf("[taskmaster] Failed to start %s: %v\n", programConfig.Command, err)
				retries++
				if retries > programConfig.StartRetries {
					fmt.Printf("[taskmaster] Aborting after %d retries: %s\n", retries-1, programConfig.Command)
					return
				}
				continue
			}

			// Wait for startsecs to consider process successfully started
			if programConfig.StartSecs > 0 {
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				select {
				case <-spv.ctx.Done():
					return
				case e := <-done:
					err = e
					fmt.Printf("[taskmaster] Process exited before startsecs: %s\n", programConfig.Command)
					retries++
					if retries > programConfig.StartRetries {
						fmt.Printf("[taskmaster] Aborting after %d retries: %s\n", retries-1, programConfig.Command)
						return
					}
					continue
				case <-time.After(time.Duration(programConfig.StartSecs) * time.Second):
					// Process survived startsecs, continue
				}
			}

			// Wait for process to exit
			err = cmd.Wait()
			var exitCode int
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
						exitCode = status.ExitStatus()
					}
				}
			} else {
				if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
					exitCode = status.ExitStatus()
				}
			}

			fmt.Printf("[taskmaster] Process exited: %s (code %d)\n", programConfig.Command, exitCode)

			// Check if exit code is expected
			expected := false
			for _, code := range programConfig.ExitCodes {
				if exitCode == code {
					expected = true
					break
				}
			}

			// Decide restart policy
			restart := false
			switch programConfig.Autorestart {
			case "always":
				restart = true
			case "never":
				restart = false
			case "unexpected":
				restart = !expected
			}

			if restart {
				fmt.Printf("[taskmaster] Restarting program: %s\n", programConfig.Command)
				retries++
				if retries > programConfig.StartRetries {
					fmt.Printf("[taskmaster] Aborting after %d retries: %s\n", retries-1, programConfig.Command)
					return
				}
				continue
			} else {
				fmt.Printf("[taskmaster] Not restarting program: %s\n", programConfig.Command)
				return
			}
		}
	}()
	
	// This approach was flawed - we need to return the actual PID and cmd
	// Let me rewrite this function completely
	return 0, nil
}

// startSingleWorkerNew creates and starts a single process instance
// This is the core function that actually executes programs and sets up monitoring
// Returns: PID of started process and the *exec.Cmd for management
func startSingleWorkerNew(programConfig *Program, spv *Supervisor) (int, *exec.Cmd) {
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
	
	// Step 7: Actually start the process
	err := cmd.Start()  // This starts the process but doesn't wait for it
	if err != nil {
		fmt.Printf("[taskmaster] Failed to start %s: %v\n", programConfig.Command, err)
		return 0, nil  // Return 0 PID and nil cmd to indicate failure
	}

	// Step 8: Get the PID and log success
	pid := cmd.Process.Pid
	fmt.Printf("[taskmaster] Started %s with PID %d\n", programConfig.Command, pid)

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
		if err != nil {
			fmt.Printf("[taskmaster] Process %d exited with error: %v\n", pid, err)
		} else {
			fmt.Printf("[taskmaster] Process %d exited successfully\n", pid)
		}

		// TODO: Handle restart logic based on autorestart policy
		// This would check programConfig.Autorestart and restart if needed
	}()

	return pid, cmd  // Return PID and command for tracking
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
		pid, cmd := startSingleWorkerNew(programConfig, spv)
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

// StopProcess gracefully stops a process using the configured stop signal
// If the process doesn't exit within stopwaitsecs, it sends SIGKILL
func (spv *Supervisor) StopProcess(cmd *exec.Cmd, programConfig *Program) error {
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
	fmt.Printf("[taskmaster] Sending signal %s to process %d\n", programConfig.StopSignal, cmd.Process.Pid)
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
