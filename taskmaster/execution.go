// Package taskmaster provides process supervision and execution management
// This file contains the core supervisor logic and process lifecycle management
package taskmaster

import (
	"context" // For handling cancellation and timeouts across goroutines
	"fmt"     // For formatted I/O operations (printing status messages)
	"os"      // For file operations and process management
	"os/exec" // For executing external programs
	"strings" // For string manipulation (splitting command arguments)
	"sync"    // For synchronization primitives (WaitGroup, mutex)
	"syscall" // For system calls (signals, umask)
	"time"    // For time-based operations (delays, timeouts)
)

// Supervisor is the main component that manages all child processes
// It tracks running programs, handles their lifecycle, and provides control interface
type Supervisor struct {
	cfg             *Config                      // Configuration loaded from YAML file
	programs        map[string]map[int]*exec.Cmd // Map: program_name -> instance_id -> process
	retryCount      map[string]map[int]int       // Map: program_name -> instance_id -> retry_attempts
	manuallyStopped map[string]map[int]bool      // Map: program_name -> instance_id -> manually_stopped_flag
	ctx             context.Context              // Context for cancellation across all goroutines
	cancel          context.CancelFunc           // Function to cancel the context (stops all goroutines)
	wg              sync.WaitGroup               // Wait group to track running goroutines
	mu              sync.RWMutex                 // Read-Write mutex for thread-safe access to programs map
	logger          *Logger                      // Logger for recording events
	stateTracker    *StateTracker                // State tracker for comprehensive process monitoring
}

// ---------------- Helper functions to reduce duplication ----------------

// effectiveRetries returns a positive retry count (default 3) for a program.
func (spv *Supervisor) effectiveRetries(p *Program) int {
	if p.StartRetries > 0 {
		return p.StartRetries
	}
	return 3
}

// markProgramInstancesManuallyStopped marks all instances of a program as manually stopped with a reason.
func (spv *Supervisor) markProgramInstancesManuallyStopped(programName string, processes map[int]*exec.Cmd, reason string) {
	if processes == nil {
		return
	}
	if spv.manuallyStopped[programName] == nil {
		spv.manuallyStopped[programName] = make(map[int]bool)
	}
	for id := range processes {
		spv.manuallyStopped[programName][id] = true
		if reason != "" {
			spv.stateTracker.UpdateState(programName, id, STOPPING, reason)
		}
	}
}

// detachProgram removes a program from tracking and returns its processes snapshot (not thread-safe by itself).
func (spv *Supervisor) detachProgram(programName string) map[int]*exec.Cmd {
	processes, ok := spv.programs[programName]
	if !ok {
		return nil
	}
	snapshot := make(map[int]*exec.Cmd, len(processes))
	for id, cmd := range processes {
		snapshot[id] = cmd
	}
	delete(spv.programs, programName)
	return snapshot
}

// removeProgramInstance removes a single instance; cleans program if empty.
func (spv *Supervisor) removeProgramInstance(programName string, instanceID int) {
	if inst, ok := spv.programs[programName]; ok {
		delete(inst, instanceID)
		if len(inst) == 0 {
			delete(spv.programs, programName)
		}
	}
}

// NewSupervisor creates a new Supervisor instance with the given configuration
// It initializes the context for cancellation and the programs tracking map
func NewSupervisor(cfg *Config, logger *Logger) *Supervisor {
	ctx, cancel := context.WithCancel(context.Background())
	return &Supervisor{
		cfg:             cfg,
		programs:        make(map[string]map[int]*exec.Cmd), // Initialize empty programs map
		retryCount:      make(map[string]map[int]int),       // Initialize retry counter map
		manuallyStopped: make(map[string]map[int]bool),      // Initialize manually stopped tracking map
		ctx:             ctx,
		cancel:          cancel,
		logger:          logger,
		stateTracker:    NewStateTracker(logger), // Initialize state tracker
	}
}

// ReloadConfig reloads the configuration from file, applies differences (add, remove, restart changed)
// and leaves unchanged programs running. It mirrors supervisorctl reload behavior.
func (spv *Supervisor) ReloadConfig(configFile string) error {
	newCfg, err := LoadConfig(configFile)
	if err != nil {
		if spv.logger != nil {
			spv.logger.LogConfigReload(false, err.Error())
		}
		return err
	}

	// Swap config under lock quickly
	spv.mu.Lock()
	oldCfg := spv.cfg
	spv.cfg = newCfg
	spv.mu.Unlock()

	// Apply changes without holding lock
	spv.applyConfigChanges(oldCfg, newCfg)

	if spv.logger != nil {
		spv.logger.LogConfigReload(true, "")
	}
	return nil
}

// applyConfigChanges compares old and new configs and applies lifecycle changes.
// Removed programs are stopped (no autorestart), changed programs are restarted, new autostart ones are started.
func (spv *Supervisor) applyConfigChanges(oldCfg, newCfg *Config) {
	// Removed programs
	for name := range oldCfg.Programs {
		if _, exists := newCfg.Programs[name]; exists {
			continue
		}
		fmt.Printf("[taskmaster] Removing program %s\n", name)
		spv.mu.Lock()
		oldProgram := oldCfg.Programs[name]
		procsMap := spv.detachProgram(name)
		spv.markProgramInstancesManuallyStopped(name, procsMap, "Removed by config reload")
		spv.mu.Unlock()
		for _, p := range procsMap {
			if p != nil {
				spv.StopProcess(p, name, &oldProgram)
			}
		}
	}

	// New & changed programs
	for name, newProgram := range newCfg.Programs {
		oldProgram, existed := oldCfg.Programs[name]
		if !existed {
			if newProgram.Autostart {
				fmt.Printf("[taskmaster] Starting new program %s\n", name)
				spv.StartProgram(name, &newProgram)
			}
			continue
		}
		if !programsEqual(oldProgram, newProgram) {
			fmt.Printf("[taskmaster] Restarting modified program %s\n", name)
			spv.mu.Lock()
			procsMap := spv.detachProgram(name)
			spv.markProgramInstancesManuallyStopped(name, procsMap, "Config change restart")
			spv.mu.Unlock()
			for _, p := range procsMap {
				if p != nil {
					spv.StopProcess(p, name, &oldProgram)
				}
			}
			if newProgram.Autostart {
				spv.StartProgram(name, &newProgram)
			}
		}
	}
}

// programsEqual compares critical fields to decide if a program definition changed
func programsEqual(p1, p2 Program) bool {
	return p1.Command == p2.Command &&
		p1.NumProcs == p2.NumProcs &&
		p1.Directory == p2.Directory &&
		p1.Autostart == p2.Autostart &&
		p1.Autorestart == p2.Autorestart &&
		p1.StopSignal == p2.StopSignal
}

// Shutdown gracefully stops all running programs and waits for goroutines.
func (spv *Supervisor) Shutdown() {
	fmt.Println("[taskmaster] Graceful shutdown initiated")
	spv.mu.Lock()
	all := make(map[string]map[int]*exec.Cmd, len(spv.programs))
	for name := range spv.programs {
		all[name] = spv.detachProgram(name)
	}
	for name, procs := range all {
		spv.markProgramInstancesManuallyStopped(name, procs, "Supervisor shutdown")
	}
	spv.mu.Unlock()

	for name, procs := range all {
		cfg, ok := spv.cfg.Programs[name]
		if !ok {
			cfg = Program{StopSignal: "TERM", StopWaitSecs: 5}
		}
		for _, c := range procs {
			if c != nil {
				spv.StopProcess(c, name, &cfg)
			}
		}
	}

	// Cancel context and wait for monitor goroutines
	spv.cancel()
	spv.wg.Wait()
	if spv.logger != nil {
		spv.logger.LogTaskmasterStop()
		spv.logger.Close()
	}
	fmt.Println("[taskmaster] Shutdown complete")
}

// startSingleWorker creates and starts a single process instance
// This is the core function that actually executes programs and sets up monitoring
// Returns: PID of started process, the *exec.Cmd for management, and program name
func startSingleWorker(programName string, programConfig *Program, instanceID int, spv *Supervisor) (int, *exec.Cmd) {
	// Update state to STARTING
	spv.stateTracker.UpdateState(programName, instanceID, STARTING, "Attempting to start process")

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
	env := os.Environ() // Get current environment
	for k, v := range programConfig.Env {
		env = append(env, k+"="+v) // Add each custom env var
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
			cmd.Stdout = stdoutFile // Redirect process stdout to file
		}
	}

	// Step 5: Set up stderr redirection (same pattern as stdout)
	if programConfig.Stderr != "" {
		stderrFile, err := os.OpenFile(programConfig.Stderr, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			fmt.Printf("Failed to open stderr file %s: %v\n", programConfig.Stderr, err)
		} else {
			cmd.Stderr = stderrFile // Redirect process stderr to file
		}
	}

	// Step 6: Set umask (file permission mask) if specified
	var oldUmask int
	if programConfig.Umask != 0 {
		oldUmask = syscall.Umask(programConfig.Umask) // Set new umask, save old one
		defer syscall.Umask(oldUmask)                 // Restore old umask when function returns
	}

	fmt.Printf("[taskmaster] Starting program: %s\n", programConfig.Command)

	// Step 7: Actually start the process with retry logic
	maxStartRetries := spv.effectiveRetries(programConfig)

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

		// Update state to BACKOFF if this isn't the last attempt
		if startAttempt < maxStartRetries {
			spv.stateTracker.UpdateState(programName, instanceID, BACKOFF,
				fmt.Sprintf("Start failed (attempt %d/%d): %v", startAttempt, maxStartRetries, err))
		}

		// If this wasn't the last attempt, wait before retrying
		if startAttempt < maxStartRetries {
			retryDelay := 1 * time.Second
			fmt.Printf("[taskmaster] Retrying startup in %v...\n", retryDelay)

			select {
			case <-spv.ctx.Done():
				// Supervisor is shutting down, don't retry
				spv.stateTracker.UpdateState(programName, instanceID, STOPPED, "Supervisor shutting down")
				return 0, nil
			case <-time.After(retryDelay):
				// Delay completed, continue to next attempt
				spv.stateTracker.UpdateState(programName, instanceID, STARTING,
					fmt.Sprintf("Retrying start (attempt %d/%d)", startAttempt+1, maxStartRetries))
			}
		}
	}

	// Check if all attempts failed
	if err != nil {
		fmt.Printf("[taskmaster] Failed to start %s after %d attempts, giving up\n",
			programConfig.Command, maxStartRetries)
		spv.stateTracker.UpdateState(programName, instanceID, FATAL,
			fmt.Sprintf("Failed to start after %d attempts: %v", maxStartRetries, err))
		return 0, nil // Return 0 PID and nil cmd to indicate failure
	}

	// Step 8: Get the PID and log success
	pid := cmd.Process.Pid
	fmt.Printf("[taskmaster] Started %s with PID %d\n", programConfig.Command, pid)
	if spv.logger != nil {
		spv.logger.LogProgramStart(programName, pid, programConfig.Command)
	}

	// Update state tracker with successful start - but keep in STARTING state until startsecs validation
	spv.stateTracker.SetPID(programName, instanceID, pid)
	// Always start in STARTING state - the monitoring goroutine will transition to RUNNING
	if programConfig.StartSecs > 0 {
		spv.stateTracker.UpdateState(programName, instanceID, STARTING,
			fmt.Sprintf("Started, checking for %d seconds", programConfig.StartSecs))
	} else {
		spv.stateTracker.UpdateState(programName, instanceID, STARTING,
			"Started, checking process")
	}

	// Step 9: Define cleanup function for file handles
	// This ensures files are properly closed when process ends
	closeFiles := func() {
		if cmd.Stdout != nil {
			if file, ok := cmd.Stdout.(*os.File); ok { // Type assertion to check if it's a file
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
	spv.wg.Add(1) // Add to wait group so we can wait for all goroutines to finish
	go func() {
		defer spv.wg.Done() // Mark this goroutine as done when it exits
		defer closeFiles()  // Ensure files are closed when goroutine exits

		// Step 10a: Handle startsecs validation
		// The process must run for at least startsecs to be considered "successfully started"
		// In supervisor, processes that exit before startsecs are considered unexpected exits
		var processExitErr error
		var survivedStartsecs bool = true // Track if process survived startsecs

		if programConfig.StartSecs > 0 {
			fmt.Printf("[taskmaster] Process %d must run for %d seconds to be considered started\n", pid, programConfig.StartSecs)

			// Create channels for communication between goroutines
			processExited := make(chan struct{})
			startupTimer := time.NewTimer(time.Duration(programConfig.StartSecs) * time.Second)
			defer startupTimer.Stop()

			// Start a goroutine to wait for process exit
			go func() {
				processExitErr = cmd.Wait()
				close(processExited)
			}()

			// Race between startup timer and process exit
			select {
			case <-spv.ctx.Done():
				// Supervisor is shutting down, exit
				return
			case <-startupTimer.C:
				// Timer expired - process survived startsecs
				fmt.Printf("[taskmaster] Process %d successfully started (survived %d seconds)\n", pid, programConfig.StartSecs)
				if spv.logger != nil {
					spv.logger.Info("Process %d (%s) successfully started after %d seconds", pid, programName, programConfig.StartSecs)
				}

				// Update state to confirm running status after startsecs validation
				spv.stateTracker.UpdateState(programName, instanceID, RUNNING,
					"Process started successfully")

				// Now wait for the process to actually exit
				<-processExited
			case <-processExited:
				// Process exited before startsecs - this should be considered unexpected
				survivedStartsecs = false
				fmt.Printf("[taskmaster] Process %d exited before startsecs (%d seconds)\n", pid, programConfig.StartSecs)
				if spv.logger != nil {
					spv.logger.Warn("Process %d (%s) exited before startsecs validation (%d seconds)", pid, programName, programConfig.StartSecs)
				}

				// Update state to indicate early exit
				spv.stateTracker.UpdateState(programName, instanceID, EXITED,
					fmt.Sprintf("Process exited before %d second startup validation", programConfig.StartSecs))
			}
		} else {
			// No startsecs validation required - transition to RUNNING after brief STARTING state
			fmt.Printf("[taskmaster] Process %d started (no startsecs validation)\n", pid)
			spv.stateTracker.UpdateState(programName, instanceID, RUNNING,
				"Process started successfully")

			// Wait for process to exit
			processExitErr = cmd.Wait()
		}

		// Step 10b: Process has exited, handle the exit
		err = processExitErr

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

		// A process that exits before startsecs should be considered unexpected
		// regardless of exit code (supervisor behavior)
		if !survivedStartsecs {
			expected = false
			fmt.Printf("[taskmaster] Process %d marked as unexpected exit (failed startsecs validation)\n", pid)
		}

		// Log the exit event
		if spv.logger != nil {
			spv.logger.LogProgramExit(programName, pid, exitCode, expected)
		}

		// Update state tracker with exit information
		spv.stateTracker.SetExitStatus(programName, instanceID, exitCode, expected)
		if expected {
			spv.stateTracker.UpdateState(programName, instanceID, EXITED,
				fmt.Sprintf("Process exited normally with code %d", exitCode))
		} else {
			spv.stateTracker.UpdateState(programName, instanceID, EXITED,
				fmt.Sprintf("Process exited unexpectedly with code %d", exitCode))
		}

		// Step 10c: Handle restart logic based on autorestart policy
		// Note: startsecs doesn't prevent restarts in supervisor
		spv.handleProcessRestart(programName, programConfig, instanceID, expected, exitCode)
	}()

	return pid, cmd // Return PID and command for tracking
}

// handleProcessRestart determines if and how to restart a process based on autorestart policy
// This implements the core autorestart functionality according to supervisor behavior
func (spv *Supervisor) handleProcessRestart(programName string, programConfig *Program, instanceID int, expectedExit bool, exitCode int) {
	// Check if this process was manually stopped
	spv.mu.Lock()
	manualStop := spv.manuallyStopped[programName][instanceID]
	if manualStop {
		// Clean up the manually stopped flag since process has exited
		delete(spv.manuallyStopped[programName], instanceID)
		if len(spv.manuallyStopped[programName]) == 0 {
			delete(spv.manuallyStopped, programName)
		}
	}
	spv.mu.Unlock()

	if manualStop {
		fmt.Printf("[taskmaster] Program %s (instance %d) was manually stopped, not restarting\n", programName, instanceID)
		spv.stateTracker.UpdateState(programName, instanceID, STOPPED, "Process manually stopped")
		// Remove from tracking since it won't be restarted
		spv.mu.Lock()
		spv.removeProgramInstance(programName, instanceID)
		spv.mu.Unlock()
		return
	}

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
		// Process exited and won't be restarted - leave it in EXITED state
		// Don't change state to STOPPED since it wasn't manually stopped
		fmt.Printf("[taskmaster] Program %s (instance %d) will remain in EXITED state (policy: %s)\n",
			programName, instanceID, programConfig.Autorestart)
		spv.mu.Lock()
		spv.removeProgramInstance(programName, instanceID)
		spv.mu.Unlock()
		return
	}

	// Initialize retry tracking if needed
	spv.mu.Lock()
	if spv.retryCount[programName] == nil {
		spv.retryCount[programName] = make(map[int]int)
	}

	// Check current retry count for this instance
	currentRetries := spv.retryCount[programName][instanceID]
	maxRetries := programConfig.StartRetries
	if maxRetries <= 0 {
		maxRetries = 3 // Default retry count
	}
	spv.mu.Unlock()

	// Check if we've exceeded the retry limit
	if currentRetries >= maxRetries {
		fmt.Printf("[taskmaster] Program %s (instance %d) has exceeded retry limit (%d), entering FATAL state\n",
			programName, instanceID, maxRetries)
		spv.stateTracker.UpdateState(programName, instanceID, FATAL,
			fmt.Sprintf("Exceeded retry limit (%d attempts)", maxRetries))

		// Remove from tracking since it won't be restarted
		spv.mu.Lock()
		spv.removeProgramInstance(programName, instanceID)
		if spv.retryCount[programName] != nil {
			delete(spv.retryCount[programName], instanceID)
			if len(spv.retryCount[programName]) == 0 {
				delete(spv.retryCount, programName)
			}
		}
		spv.mu.Unlock()
		return
	}

	// Increment retry count for this restart attempt
	spv.mu.Lock()
	spv.retryCount[programName][instanceID] = currentRetries + 1
	spv.mu.Unlock()

	// Add a small delay before restarting to prevent rapid restart loops
	restartDelay := 1 * time.Second
	fmt.Printf("[taskmaster] Restarting program %s (instance %d) in %v (attempt %d/%d)\n",
		programName, instanceID, restartDelay, currentRetries+1, maxRetries)

	// Update state to indicate restart is pending
	spv.stateTracker.UpdateState(programName, instanceID, BACKOFF,
		fmt.Sprintf("Waiting %v before restart (attempt %d/%d)", restartDelay, currentRetries+1, maxRetries))

	// Log the restart attempt
	if spv.logger != nil {
		spv.logger.LogProgramRestart(programName, "autorestart")
	}

	// Wait before restarting
	select {
	case <-spv.ctx.Done():
		// Supervisor is shutting down, don't restart
		spv.stateTracker.UpdateState(programName, instanceID, STOPPED, "Supervisor shutting down")
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
		// startSingleWorker already handles start failures and sets FATAL state if needed
		// If startSingleWorker returns nil, the process is already in FATAL state
	}
}

// StartProgram starts all instances of a program according to its numprocs setting
// This is called by the shell when user types "start program_name"
func (spv *Supervisor) StartProgram(programName string, programConfig *Program) {
	// Initialize the program's process map if it doesn't exist
	if spv.programs[programName] == nil {
		spv.programs[programName] = make(map[int]*exec.Cmd)
	}

	// Clear any manually stopped flags for this program since we're starting it manually
	// This allows autorestart to work again after manual start
	spv.mu.Lock()
	if spv.manuallyStopped[programName] != nil {
		delete(spv.manuallyStopped, programName)
		fmt.Printf("[taskmaster] Cleared manually stopped flags for program %s\n", programName)
	}

	// Reset retry counts for manual start - give the program a fresh chance
	if spv.retryCount[programName] != nil {
		delete(spv.retryCount, programName)
		fmt.Printf("[taskmaster] Reset retry counts for program %s (manual start)\n", programName)
	}
	spv.mu.Unlock()

	// Register all process instances in the state tracker and start them
	// Each process gets an instance ID: 0, 1, 2, etc.
	for i := 0; i < programConfig.NumProcs; i++ {
		// Register the process in state tracker before starting
		spv.stateTracker.RegisterProcess(programName, i, programConfig)

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
	// Iterate through all configured programs
	for name, programConfig := range spv.cfg.Programs {
		if !programConfig.Autostart {
			continue // Skip programs that shouldn't auto-start
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
	spv.mu.Lock()
	spv.markProgramInstancesManuallyStopped(programName, processes, "Manual stop requested")
	procs := spv.detachProgram(programName)
	spv.mu.Unlock()
	for _, proc := range procs {
		if proc != nil {
			if err := spv.StopProcess(proc, programName, programConfig); err != nil {
				if spv.logger != nil {
					spv.logger.LogError("stopping program "+programName, err)
				}
			}
		}
	}
	return nil
}

// GetProcessState returns the current state of a specific process instance
func (spv *Supervisor) GetProcessState(programName string, instanceID int) (*ProcessInfo, bool) {
	return spv.stateTracker.GetProcessInfo(programName, instanceID)
}

// GetAllProcessStates returns the current state of all tracked processes
func (spv *Supervisor) GetAllProcessStates() map[string]*ProcessInfo {
	return spv.stateTracker.GetAllProcesses()
}

// GetProgramStates returns all instances of a specific program
func (spv *Supervisor) GetProgramStates(programName string) []*ProcessInfo {
	return spv.stateTracker.GetProcessesByName(programName)
}

// IsProcessRunning checks if a specific process instance is running
func (spv *Supervisor) IsProcessRunning(programName string, instanceID int) bool {
	return spv.stateTracker.IsProcessRunning(programName, instanceID)
}

// GetRunningCount returns the number of running instances for a program
func (spv *Supervisor) GetRunningCount(programName string) int {
	return spv.stateTracker.GetRunningCount(programName)
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
		sig = syscall.SIGHUP // Hangup signal
	case "INT":
		sig = syscall.SIGINT // Interrupt signal (Ctrl+C)
	case "QUIT":
		sig = syscall.SIGQUIT // Quit signal (Ctrl+\)
	case "KILL":
		sig = syscall.SIGKILL // Kill signal (cannot be caught)
	case "USR1":
		sig = syscall.SIGUSR1 // User-defined signal 1
	case "USR2":
		sig = syscall.SIGUSR2 // User-defined signal 2
	case "TERM":
		sig = syscall.SIGTERM // Termination signal (default)
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
		// Poll the process to see if it's still running instead of calling Wait()
		// since the monitoring goroutine is already calling Wait()
		for {
			err := cmd.Process.Signal(syscall.Signal(0))
			if err != nil {
				// Process no longer exists
				done <- nil
				return
			}
			time.Sleep(100 * time.Millisecond) // Check every 100ms
		}
	}()

	// Step 4: Race between timeout and process exit
	select {
	case <-time.After(time.Duration(waitSecs) * time.Second):
		// Timeout expired - process didn't exit gracefully, force kill it
		fmt.Printf("[taskmaster] Process %d did not exit after %d seconds, killing\n", cmd.Process.Pid, waitSecs)
		err := cmd.Process.Kill() // Send SIGKILL (cannot be ignored)
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
