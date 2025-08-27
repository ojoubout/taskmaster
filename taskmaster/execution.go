package taskmaster

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Supervisor struct {
	cfg             *Config
	programs        map[string]map[int]*exec.Cmd
	retryCount      map[string]map[int]int
	manuallyStopped map[string]map[int]bool
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	mu              sync.RWMutex
	logger          *Logger
	stateTracker    *StateTracker
}

func (spv *Supervisor) effectiveRetries(p *Program) int {
	if p.StartRetries > 0 {
		return p.StartRetries
	}
	return 3
}

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

func (spv *Supervisor) removeProgramInstance(programName string, instanceID int) {
	if inst, ok := spv.programs[programName]; ok {
		delete(inst, instanceID)
		if len(inst) == 0 {
			delete(spv.programs, programName)
		}
	}
}

func NewSupervisor(cfg *Config, logger *Logger) *Supervisor {
	ctx, cancel := context.WithCancel(context.Background())
	return &Supervisor{
		cfg:             cfg,
		programs:        make(map[string]map[int]*exec.Cmd),
		retryCount:      make(map[string]map[int]int),
		manuallyStopped: make(map[string]map[int]bool),
		ctx:             ctx,
		cancel:          cancel,
		logger:          logger,
		stateTracker:    NewStateTracker(logger),
	}
}

func (spv *Supervisor) ReloadConfig(configFile string) error {
	newCfg, err := LoadConfig(configFile)
	if err != nil {
		if spv.logger != nil {
			spv.logger.LogConfigReload(false, err.Error())
		}
		return err
	}

	spv.mu.Lock()
	oldCfg := spv.cfg
	spv.cfg = newCfg
	spv.mu.Unlock()

	spv.applyConfigChanges(oldCfg, newCfg)

	if spv.logger != nil {
		spv.logger.LogConfigReload(true, "")
	}
	return nil
}

func (spv *Supervisor) applyConfigChanges(oldCfg, newCfg *Config) {
	for name := range oldCfg.Programs {
		if _, exists := newCfg.Programs[name]; exists {
			continue
		}
		if spv.logger != nil {
			spv.logger.Info("Removing program %s from configuration", name)
		}
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
	for name, newProgram := range newCfg.Programs {
		oldProgram, existed := oldCfg.Programs[name]
		if !existed {
			if newProgram.Autostart {
				if spv.logger != nil {
					spv.logger.Info("Starting new program %s", name)
				}
				spv.StartProgram(name, &newProgram)
			}
			continue
		}
		if !programsEqual(oldProgram, newProgram) {
			if spv.logger != nil {
				spv.logger.Info("Restarting modified program %s", name)
			}
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

func programsEqual(p1, p2 Program) bool {
	if p1.Command != p2.Command ||
		p1.NumProcs != p2.NumProcs ||
		p1.Directory != p2.Directory ||
		p1.Autostart != p2.Autostart ||
		p1.Autorestart != p2.Autorestart ||
		p1.StopSignal != p2.StopSignal ||
		p1.StopWaitSecs != p2.StopWaitSecs ||
		p1.StartSecs != p2.StartSecs ||
		p1.StartRetries != p2.StartRetries ||
		p1.Umask != p2.Umask ||
		p1.Stdout != p2.Stdout ||
		p1.Stderr != p2.Stderr {
		return false
	}

	if len(p1.ExitCodes) != len(p2.ExitCodes) {
		return false
	}
	c1 := append([]int(nil), p1.ExitCodes...)
	c2 := append([]int(nil), p2.ExitCodes...)
	sort.Ints(c1)
	sort.Ints(c2)
	for i := range c1 {
		if c1[i] != c2[i] {
			return false
		}
	}

	if len(p1.Env) != len(p2.Env) {
		return false
	}
	for k, v := range p1.Env {
		if p2.Env[k] != v {
			return false
		}
	}
	return true
}

func (spv *Supervisor) Shutdown() {
	if spv.logger != nil {
		spv.logger.Info("Graceful shutdown initiated")
	}
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

	spv.cancel()
	spv.wg.Wait()
	if spv.logger != nil {
		spv.logger.LogTaskmasterStop()
		spv.logger.Close()
	}
	fmt.Println("[taskmaster] Shutdown complete")
}

func startSingleWorker(programName string, programConfig *Program, instanceID int, spv *Supervisor) (int, *exec.Cmd) {
	spv.stateTracker.UpdateState(programName, instanceID, STARTING, "Attempting to start process")

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
		if err != nil {
			if spv.logger != nil {
				spv.logger.Error("Failed to open stdout file %s: %v", programConfig.Stdout, err)
			}
		} else {
			cmd.Stdout = stdoutFile
		}
	}

	if programConfig.Stderr != "" {
		stderrFile, err := os.OpenFile(programConfig.Stderr, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			if spv.logger != nil {
				spv.logger.Error("Failed to open stderr file %s: %v", programConfig.Stderr, err)
			}
		} else {
			cmd.Stderr = stderrFile
		}
	}

	var oldUmask int
	if programConfig.Umask != 0 {
		oldUmask = syscall.Umask(programConfig.Umask)
		defer syscall.Umask(oldUmask)
	}

	maxStartRetries := spv.effectiveRetries(programConfig)

	var err error
	var startAttempt int

	for startAttempt = 1; startAttempt <= maxStartRetries; startAttempt++ {
		if startAttempt > 1 {
			parts := strings.Fields(programConfig.Command)
			cmd = exec.CommandContext(spv.ctx, parts[0], parts[1:]...)

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
					if spv.logger != nil {
						spv.logger.Error("Failed to open stdout file %s during retry: %v", programConfig.Stdout, err)
					}
				} else {
					cmd.Stdout = stdoutFile
				}
			}

			if programConfig.Stderr != "" {
				stderrFile, err := os.OpenFile(programConfig.Stderr, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
				if err != nil {
					if spv.logger != nil {
						spv.logger.Error("Failed to open stderr file %s during retry: %v", programConfig.Stderr, err)
					}
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
			break
		}

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
			if spv.logger != nil {
				spv.logger.Debug("Retrying startup in %v (attempt %d/%d)", retryDelay, startAttempt+1, maxStartRetries)
			}

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

	if err != nil {
		if spv.logger != nil {
			spv.logger.Error("Failed to start %s after %d attempts, giving up", programConfig.Command, maxStartRetries)
		}
		spv.stateTracker.UpdateState(programName, instanceID, FATAL,
			fmt.Sprintf("Failed to start after %d attempts: %v", maxStartRetries, err))
		return 0, nil
	}

	pid := cmd.Process.Pid
	if spv.logger != nil {
		spv.logger.LogProgramStart(programName, pid, programConfig.Command)
	}

	spv.stateTracker.SetPID(programName, instanceID, pid)
	if programConfig.StartSecs > 0 {
		spv.stateTracker.UpdateState(programName, instanceID, STARTING,
			fmt.Sprintf("Started, checking for %d seconds", programConfig.StartSecs))
	} else {
		spv.stateTracker.UpdateState(programName, instanceID, STARTING,
			"Started, checking process")
	}

	// Step 9: Define cleanup function for file handles
	closeFiles := func() {
		if cmd.Stdout != nil {
			if file, ok := cmd.Stdout.(*os.File); ok {
				file.Close()
			}
		}
		if cmd.Stderr != nil {
			if file, ok := cmd.Stderr.(*os.File); ok {
				file.Close()
			}
		}
	}

	spv.wg.Add(1)
	go func() {
		defer spv.wg.Done()
		defer closeFiles()

		var processExitErr error
		var survivedStartsecs bool = true

		if programConfig.StartSecs > 0 {
			if spv.logger != nil {
				spv.logger.Debug("Process %d (%s) must run for %d seconds to be considered started", pid, programName, programConfig.StartSecs)
			}

			processExited := make(chan struct{})
			startupTimer := time.NewTimer(time.Duration(programConfig.StartSecs) * time.Second)
			defer startupTimer.Stop()

			// Start a goroutine to wait for process exit
			go func() {
				processExitErr = cmd.Wait()
				close(processExited)
			}()

			select {
			case <-spv.ctx.Done():
				return
			case <-startupTimer.C:
				if spv.logger != nil {
					spv.logger.Debug("Process %d (%s) survived startsecs validation (%d seconds)", pid, programName, programConfig.StartSecs)
				}

				spv.stateTracker.UpdateState(programName, instanceID, RUNNING,
					"Process started successfully")

				<-processExited
			case <-processExited:
				survivedStartsecs = false
				if spv.logger != nil {
					spv.logger.Warn("Process %d (%s) exited before startsecs validation (%d seconds)", pid, programName, programConfig.StartSecs)
				}

				spv.stateTracker.UpdateState(programName, instanceID, EXITED,
					fmt.Sprintf("Process exited before %d second startup validation", programConfig.StartSecs))
			}
		} else {
			if spv.logger != nil {
				spv.logger.Debug("Process %d (%s) started (no startsecs validation)", pid, programName)
			}
			spv.stateTracker.UpdateState(programName, instanceID, RUNNING,
				"Process started successfully")

			processExitErr = cmd.Wait()
		}

		err = processExitErr

		exitCode := 0
		expected := false

		if err != nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				exitCode = exitError.ExitCode()
			}
			if spv.logger != nil {
				spv.logger.Debug("Process %d (%s) exited with error: %v", pid, programName, err)
			}
		} else {
			// Even successful exits (code 0) must be validated against expected codes
			exitCode = 0
			if spv.logger != nil {
				spv.logger.Debug("Process %d (%s) exited with code 0", pid, programName)
			}
		}

		for _, expectedCode := range programConfig.ExitCodes {
			if exitCode == expectedCode {
				expected = true
				break
			}
		}

		if expected {
			if spv.logger != nil {
				spv.logger.Debug("Exit code %d is expected for program %s", exitCode, programName)
			}
		} else {
			if spv.logger != nil {
				spv.logger.Debug("Exit code %d is unexpected for program %s (expected: %v)",
					exitCode, programName, programConfig.ExitCodes)
			}
		}

		if !survivedStartsecs {
			expected = false
			if spv.logger != nil {
				spv.logger.Debug("Process %d (%s) marked as unexpected exit (failed startsecs validation)", pid, programName)
			}
		}

		if spv.logger != nil {
			spv.logger.LogProgramExit(programName, pid, exitCode, expected)
		}

		spv.stateTracker.SetExitStatus(programName, instanceID, exitCode, expected)
		if expected {
			spv.stateTracker.UpdateState(programName, instanceID, EXITED,
				fmt.Sprintf("Process exited normally with code %d", exitCode))
		} else {
			spv.stateTracker.UpdateState(programName, instanceID, EXITED,
				fmt.Sprintf("Process exited unexpectedly with code %d", exitCode))
		}

		spv.handleProcessRestart(programName, programConfig, instanceID, expected, exitCode)
	}()

	return pid, cmd
}

func (spv *Supervisor) handleProcessRestart(programName string, programConfig *Program, instanceID int, expectedExit bool, exitCode int) {
	spv.mu.Lock()
	manualStop := spv.manuallyStopped[programName][instanceID]
	if manualStop {
		delete(spv.manuallyStopped[programName], instanceID)
		if len(spv.manuallyStopped[programName]) == 0 {
			delete(spv.manuallyStopped, programName)
		}
	}
	spv.mu.Unlock()

	if manualStop {
		if spv.logger != nil {
			spv.logger.Info("Program %s (instance %d) was manually stopped, not restarting", programName, instanceID)
		}
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
		if spv.logger != nil {
			spv.logger.Info("Program %s (instance %d) will be restarted (policy: always)", programName, instanceID)
		}
	case "never":
		// Never restart
		shouldRestart = false
		if spv.logger != nil {
			spv.logger.Info("Program %s (instance %d) will not be restarted (policy: never)", programName, instanceID)
		}
	case "unexpected", "":
		shouldRestart = !expectedExit
		if shouldRestart {
			if spv.logger != nil {
				spv.logger.Info("Program %s (instance %d) will be restarted (unexpected exit with code %d)", programName, instanceID, exitCode)
			}
		} else {
			if spv.logger != nil {
				spv.logger.Info("Program %s (instance %d) will not be restarted (expected exit with code %d)", programName, instanceID, exitCode)
			}
		}
	default:
		shouldRestart = false
		if spv.logger != nil {
			spv.logger.Warn("Program %s (instance %d) has invalid autorestart policy '%s', treating as 'never'", programName, instanceID, programConfig.Autorestart)
		}
	}

	if !shouldRestart {
		if spv.logger != nil {
			spv.logger.Info("Program %s (instance %d) will remain in EXITED state (policy: %s)",
				programName, instanceID, programConfig.Autorestart)
		}
		spv.mu.Lock()
		spv.removeProgramInstance(programName, instanceID)
		spv.mu.Unlock()
		return
	}

	spv.mu.Lock()
	if spv.retryCount[programName] == nil {
		spv.retryCount[programName] = make(map[int]int)
	}

	currentRetries := spv.retryCount[programName][instanceID]
	maxRetries := programConfig.StartRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}
	spv.mu.Unlock()

	if currentRetries >= maxRetries {
		if spv.logger != nil {
			spv.logger.Warn("Program %s (instance %d) has exceeded retry limit (%d), entering FATAL state",
				programName, instanceID, maxRetries)
		}
		spv.stateTracker.UpdateState(programName, instanceID, FATAL,
			fmt.Sprintf("Exceeded retry limit (%d attempts)", maxRetries))

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

	spv.mu.Lock()
	spv.retryCount[programName][instanceID] = currentRetries + 1
	spv.mu.Unlock()

	restartDelay := 1 * time.Second
	if spv.logger != nil {
		spv.logger.Info("Restarting program %s (instance %d) in %v (attempt %d/%d)",
			programName, instanceID, restartDelay, currentRetries+1, maxRetries)
	}

	spv.stateTracker.UpdateState(programName, instanceID, BACKOFF,
		fmt.Sprintf("Waiting %v before restart (attempt %d/%d)", restartDelay, currentRetries+1, maxRetries))

	if spv.logger != nil {
		spv.logger.LogProgramRestart(programName, "autorestart")
	}

	select {
	case <-spv.ctx.Done():
		spv.stateTracker.UpdateState(programName, instanceID, STOPPED, "Supervisor shutting down")
		return
	case <-time.After(restartDelay):
	}

	pid, newCmd := startSingleWorker(programName, programConfig, instanceID, spv)
	if newCmd != nil {
		spv.mu.Lock()
		if spv.programs[programName] == nil {
			spv.programs[programName] = make(map[int]*exec.Cmd)
		}
		spv.programs[programName][instanceID] = newCmd
		spv.mu.Unlock()

		if spv.logger != nil {
			spv.logger.Info("Successfully restarted program %s (instance %d) with new PID %d", programName, instanceID, pid)
		}
	} else {
		if spv.logger != nil {
			spv.logger.Error("Failed to restart program %s (instance %d)", programName, instanceID)
		}
	}
}

func (spv *Supervisor) StartProgram(programName string, programConfig *Program) {
	if spv.programs[programName] == nil {
		spv.programs[programName] = make(map[int]*exec.Cmd)
	}

	spv.mu.Lock()
	if spv.manuallyStopped[programName] != nil {
		delete(spv.manuallyStopped, programName)
		if spv.logger != nil {
			spv.logger.Debug("Cleared manually stopped flags for program %s", programName)
		}
	}

	if spv.retryCount[programName] != nil {
		delete(spv.retryCount, programName)
		if spv.logger != nil {
			spv.logger.Debug("Reset retry counts for program %s (manual start)", programName)
		}
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
			if spv.logger != nil {
				spv.logger.Debug("Stored process %d for program %s (instance %d)", pid, programName, i)
			}
		}
	}
}

// RunInitialState starts all programs that have autostart=true
func RunInitialState(spv *Supervisor) {
	for name, programConfig := range spv.cfg.Programs {
		if !programConfig.Autostart {
			continue
		}
		spv.StartProgram(name, &programConfig)
	}
}

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

func (spv *Supervisor) GetProcessState(programName string, instanceID int) (*ProcessInfo, bool) {
	return spv.stateTracker.GetProcessInfo(programName, instanceID)
}

func (spv *Supervisor) GetAllProcessStates() map[string]*ProcessInfo {
	return spv.stateTracker.GetAllProcesses()
}

func (spv *Supervisor) GetProgramStates(programName string) []*ProcessInfo {
	return spv.stateTracker.GetProcessesByName(programName)
}

func (spv *Supervisor) IsProcessRunning(programName string, instanceID int) bool {
	return spv.stateTracker.IsProcessRunning(programName, instanceID)
}

func (spv *Supervisor) GetRunningCount(programName string) int {
	return spv.stateTracker.GetRunningCount(programName)
}

// StopProcess gracefully stops a process using the configured stop signal
// If the process doesn't exit within stopwaitsecs, it sends SIGKILL
func (spv *Supervisor) StopProcess(cmd *exec.Cmd, programName string, programConfig *Program) error {
	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("process not running")
	}

	sig := syscall.SIGTERM
	switch programConfig.StopSignal {
	case "HUP":
		sig = syscall.SIGHUP
	case "INT":
		sig = syscall.SIGINT
	case "QUIT":
		sig = syscall.SIGQUIT
	case "KILL":
		sig = syscall.SIGKILL
	case "USR1":
		sig = syscall.SIGUSR1
	case "USR2":
		sig = syscall.SIGUSR2
	case "TERM":
		sig = syscall.SIGTERM
	}

	pid := cmd.Process.Pid
	signalName := programConfig.StopSignal
	if signalName == "" {
		signalName = "TERM"
	}

	if spv.logger != nil {
		spv.logger.LogProgramStop(programName, pid, signalName)
	}

	err := cmd.Process.Signal(sig)
	if err != nil {
		return fmt.Errorf("failed to send signal: %v", err)
	}

	waitSecs := programConfig.StopWaitSecs
	if waitSecs <= 0 {
		waitSecs = 5
	}

	done := make(chan error, 1)
	go func() {
		for {
			err := cmd.Process.Signal(syscall.Signal(0))
			if err != nil {
				done <- nil
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()

	select {
	case <-time.After(time.Duration(waitSecs) * time.Second):
		if spv.logger != nil {
			spv.logger.Warn("Process %d did not exit after %d seconds, sending SIGKILL", cmd.Process.Pid, waitSecs)
		}
		err := cmd.Process.Kill()
		if err != nil {
			return fmt.Errorf("failed to kill process: %v", err)
		}
		<-done
		return nil
	case err := <-done:
		if spv.logger != nil {
			spv.logger.Debug("Process %d exited after signal", cmd.Process.Pid)
		}
		return err
	}
}
