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
					defer stdoutFile.Close()
				} else {
					fmt.Printf("failed to open stdout file %s: %v\n", programConfig.Stdout, err)
				}
			}

			if programConfig.Stderr != "" {
				stderrFile, err := os.OpenFile(programConfig.Stderr, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
				if err == nil {
					cmd.Stderr = stderrFile
					defer stderrFile.Close()
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
	fmt.Println(programConfig)
	return 0, nil
}

func StartProgram(program *map[int]*exec.Cmd, spv *Supervisor, programConfig *Program) {
	for i := 0; i < programConfig.NumProcs; i++ {
		pid, cmd := startSingleWorker(programConfig, spv)
		(*program)[pid] = cmd
	}
}

func RunInitialState(spv *Supervisor) {
	for name, programConfig := range spv.cfg.Programs {
		if !programConfig.Autostart {
			continue
		}
		if spv.programs[name] == nil {
			spv.programs[name] = map[int]*exec.Cmd{}
		}
		prog := spv.programs[name]
		StartProgram(&prog, spv, &programConfig)
		spv.programs[name] = prog
	}
}

// StopProcess sends the stopsignal to the process, waits for stopwaitsecs, and force kills if not exited.
func StopProcess(cmd *exec.Cmd, programConfig *Program) error {
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
