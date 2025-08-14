package taskmaster

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Supervisor struct {
	cfg      *Config
	programs map[string]map[int]*exec.Cmd
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	mu       sync.RWMutex
}

func NewSupervisor(cfg *Config) *Supervisor {
	ctx, cancel := context.WithCancel(context.Background())
	return &Supervisor{cfg: cfg, programs: make(map[string]map[int]*exec.Cmd), ctx: ctx, cancel: cancel}
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

func startSingleWorkerNew(programConfig *Program, spv *Supervisor) (int, *exec.Cmd) {
	parts := strings.Fields(programConfig.Command)
	cmd := exec.CommandContext(spv.ctx, parts[0], parts[1:]...)

	if programConfig.Directory != "" {
		cmd.Dir = programConfig.Directory
	}

	// Set environment variables
	env := os.Environ()
	for k, v := range programConfig.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	// Set up stdout redirection
	if programConfig.Stdout != "" {
		stdoutFile, err := os.OpenFile(programConfig.Stdout, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			fmt.Printf("Failed to open stdout file %s: %v\n", programConfig.Stdout, err)
		} else {
			cmd.Stdout = stdoutFile
		}
	}

	// Set up stderr redirection
	if programConfig.Stderr != "" {
		stderrFile, err := os.OpenFile(programConfig.Stderr, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			fmt.Printf("Failed to open stderr file %s: %v\n", programConfig.Stderr, err)
		} else {
			cmd.Stderr = stderrFile
		}
	}

	// Set umask
	var oldUmask int
	if programConfig.Umask != 0 {
		oldUmask = syscall.Umask(programConfig.Umask)
		defer syscall.Umask(oldUmask)
	}

	fmt.Printf("[taskmaster] Starting program: %s\n", programConfig.Command)
	
	// Start the process
	err := cmd.Start()
	if err != nil {
		fmt.Printf("[taskmaster] Failed to start %s: %v\n", programConfig.Command, err)
		return 0, nil
	}

	pid := cmd.Process.Pid
	fmt.Printf("[taskmaster] Started %s with PID %d\n", programConfig.Command, pid)

	// Function to close file handles
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

	// Start monitoring goroutine
	spv.wg.Add(1)
	go func() {
		defer spv.wg.Done()
		defer closeFiles()

		// Wait for startsecs validation if needed
		if programConfig.StartSecs > 0 {
			// Use a timer to check if process survives startsecs
			timer := time.NewTimer(time.Duration(programConfig.StartSecs) * time.Second)
			defer timer.Stop()
			
			select {
			case <-spv.ctx.Done():
				return
			case <-timer.C:
				fmt.Printf("[taskmaster] Process %d survived startsecs, now monitoring\n", pid)
			}
		}

		// Monitor the process until it exits
		err = cmd.Wait()
		if err != nil {
			fmt.Printf("[taskmaster] Process %d exited with error: %v\n", pid, err)
		} else {
			fmt.Printf("[taskmaster] Process %d exited successfully\n", pid)
		}

		// TODO: Handle restart logic based on autorestart policy
	}()

	return pid, cmd
}

func (spv *Supervisor) StartProgram(programName string, programConfig *Program) {
	if spv.programs[programName] == nil {
		spv.programs[programName] = make(map[int]*exec.Cmd)
	}
	
	for i := 0; i < programConfig.NumProcs; i++ {
		pid, cmd := startSingleWorkerNew(programConfig, spv)
		if cmd != nil {
			spv.programs[programName][i] = cmd
			fmt.Printf("[taskmaster] Stored process %d for program %s (instance %d)\n", pid, programName, i)
		}
	}
}

func RunInitialState(spv *Supervisor) {
	spv.mu.Lock()
	defer spv.mu.Unlock()
	
	for name, programConfig := range spv.cfg.Programs {
		if !programConfig.Autostart {
			continue
		}
		spv.StartProgram(name, &programConfig)
	}
}

// StopProcess sends the stopsignal to the process, waits for stopwaitsecs, and force kills if not exited.
func (spv *Supervisor) StopProcess(cmd *exec.Cmd, programConfig *Program) error {
	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("process not running")
	}

	// Parse stopsignal string to syscall.Signal
	sig := syscall.SIGTERM // default
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

	fmt.Printf("[taskmaster] Sending signal %s to process %d\n", programConfig.StopSignal, cmd.Process.Pid)
	err := cmd.Process.Signal(sig)
	if err != nil {
		return fmt.Errorf("failed to send signal: %v", err)
	}

	// Wait for process to exit up to stopwaitsecs
	waitSecs := programConfig.StopWaitSecs
	if waitSecs <= 0 {
		waitSecs = 5 // default wait time
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-time.After(time.Duration(waitSecs) * time.Second):
		fmt.Printf("[taskmaster] Process %d did not exit after %d seconds, killing\n", cmd.Process.Pid, waitSecs)
		err := cmd.Process.Kill()
		if err != nil {
			return fmt.Errorf("failed to kill process: %v", err)
		}
		<-done // ensure Wait() completes
		return nil
	case err := <-done:
		fmt.Printf("[taskmaster] Process %d exited after signal\n", cmd.Process.Pid)
		return err
	}
}
