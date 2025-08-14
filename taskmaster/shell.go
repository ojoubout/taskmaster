package taskmaster

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
)

type Shell struct {
	supervisor *Supervisor
	running    bool
	mu         sync.RWMutex
}

func NewShell(supervisor *Supervisor) *Shell {
	return &Shell{
		supervisor: supervisor,
		running:    true,
	}
}

func (s *Shell) Start() {
	fmt.Println("Taskmaster control shell started. Type 'help' for commands.")
	scanner := bufio.NewScanner(os.Stdin)
	
	for s.running {
		fmt.Print("taskmaster> ")
		if !scanner.Scan() {
			break
		}
		
		command := strings.TrimSpace(scanner.Text())
		if command == "" {
			continue
		}
		
		s.processCommand(command)
	}
	
	if err := scanner.Err(); err != nil {
		fmt.Printf("Error reading input: %v\n", err)
	}
}

func (s *Shell) processCommand(command string) {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return
	}
	
	cmd := strings.ToLower(parts[0])
	args := parts[1:]
	
	switch cmd {
	case "help", "h":
		s.showHelp()
	case "status":
		s.showStatus(args)
	case "start":
		s.startProgram(args)
	case "stop":
		s.stopProgram(args)
	case "restart":
		s.restartProgram(args)
	case "reload":
		s.reloadConfig()
	case "quit", "exit":
		s.quit()
	default:
		fmt.Printf("Unknown command: %s. Type 'help' for available commands.\n", cmd)
	}
}

func (s *Shell) showHelp() {
	fmt.Println("Available commands:")
	fmt.Println("  help, h          - Show this help message")
	fmt.Println("  status [program] - Show status of all programs or specific program")
	fmt.Println("  start <program>  - Start a program")
	fmt.Println("  stop <program>   - Stop a program")
	fmt.Println("  restart <program>- Restart a program")
	fmt.Println("  reload           - Reload configuration file")
	fmt.Println("  quit, exit       - Exit taskmaster")
}

func (s *Shell) showStatus(args []string) {
	s.supervisor.mu.RLock()
	defer s.supervisor.mu.RUnlock()
	
	if len(args) == 0 {
		// Show status of all programs
		fmt.Printf("%-15s %-10s %-8s %-s\n", "PROGRAM", "STATUS", "PID", "COMMAND")
		fmt.Println(strings.Repeat("-", 60))
		
		for name, program := range s.supervisor.cfg.Programs {
			processes, exists := s.supervisor.programs[name]
			if !exists || len(processes) == 0 {
				fmt.Printf("%-15s %-10s %-8s %-s\n", name, "STOPPED", "-", program.Command)
				continue
			}
			
			for i, proc := range processes {
				status := "UNKNOWN"
				pid := "-"
				
				if proc != nil && proc.Process != nil {
					pid = fmt.Sprintf("%d", proc.Process.Pid)
					// Check if process is still running
					if proc.ProcessState == nil {
						// Process hasn't exited yet, check if it's actually running
						err := proc.Process.Signal(syscall.Signal(0))
						if err == nil {
							status = "RUNNING"
						} else {
							status = "STOPPED"
						}
					} else {
						status = "EXITED"
					}
				} else {
					status = "STOPPED"
				}
				
				programName := name
				if i > 0 {
					programName = fmt.Sprintf("%s_%d", name, i)
				}
				
				fmt.Printf("%-15s %-10s %-8s %-s\n", programName, status, pid, program.Command)
			}
		}
	} else {
		// Show status of specific program
		programName := args[0]
		program, exists := s.supervisor.cfg.Programs[programName]
		if !exists {
			fmt.Printf("Program '%s' not found in configuration.\n", programName)
			return
		}
		
		processes, running := s.supervisor.programs[programName]
		fmt.Printf("Program: %s\n", programName)
		fmt.Printf("Command: %s\n", program.Command)
		fmt.Printf("NumProcs: %d\n", program.NumProcs)
		fmt.Printf("Autostart: %t\n", program.Autostart)
		fmt.Printf("Autorestart: %s\n", program.Autorestart)
		
		if !running || len(processes) == 0 {
			fmt.Println("Status: STOPPED")
		} else {
			fmt.Printf("Running processes: %d\n", len(processes))
			for i, proc := range processes {
				if proc != nil && proc.Process != nil {
					status := "RUNNING"
					if proc.ProcessState != nil {
						status = "EXITED"
					}
					fmt.Printf("  Process %d: PID %d, Status: %s\n", i, proc.Process.Pid, status)
				}
			}
		}
	}
}

func (s *Shell) startProgram(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: start <program>")
		return
	}
	
	programName := args[0]
	program, exists := s.supervisor.cfg.Programs[programName]
	if !exists {
		fmt.Printf("Program '%s' not found in configuration.\n", programName)
		return
	}
	
	s.supervisor.mu.Lock()
	defer s.supervisor.mu.Unlock()
	
	// Check if already running
	if processes, running := s.supervisor.programs[programName]; running && len(processes) > 0 {
		fmt.Printf("Program '%s' is already running.\n", programName)
		return
	}
	
	fmt.Printf("Starting program '%s'...\n", programName)
	s.supervisor.StartProgram(programName, &program)
	fmt.Printf("Program '%s' started.\n", programName)
}

func (s *Shell) stopProgram(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: stop <program>")
		return
	}
	
	programName := args[0]
	program, exists := s.supervisor.cfg.Programs[programName]
	if !exists {
		fmt.Printf("Program '%s' not found in configuration.\n", programName)
		return
	}
	
	s.supervisor.mu.Lock()
	defer s.supervisor.mu.Unlock()
	
	processes, running := s.supervisor.programs[programName]
	if !running || len(processes) == 0 {
		fmt.Printf("Program '%s' is not running.\n", programName)
		return
	}
	
	fmt.Printf("Stopping program '%s'...\n", programName)
	for _, proc := range processes {
		if proc != nil {
			s.supervisor.StopProcess(proc, &program)
		}
	}
	
	// Remove from running programs
	delete(s.supervisor.programs, programName)
	fmt.Printf("Program '%s' stopped.\n", programName)
}

func (s *Shell) restartProgram(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: restart <program>")
		return
	}
	
	programName := args[0]
	_, exists := s.supervisor.cfg.Programs[programName]
	if !exists {
		fmt.Printf("Program '%s' not found in configuration.\n", programName)
		return
	}
	
	fmt.Printf("Restarting program '%s'...\n", programName)
	
	// Stop first
	s.stopProgram(args)
	
	// Small delay to ensure cleanup
	fmt.Print("Waiting for cleanup...")
	for i := 0; i < 3; i++ {
		fmt.Print(".")
		// time.Sleep(500 * time.Millisecond)
	}
	fmt.Println()
	
	// Start again
	s.startProgram(args)
}

func (s *Shell) reloadConfig() {
	fmt.Println("Reloading configuration...")
	
	// Load new config
	newCfg, err := LoadConfig("taskmaster.conf")
	if err != nil {
		fmt.Printf("Error reloading config: %v\n", err)
		return
	}
	
	s.supervisor.mu.Lock()
	defer s.supervisor.mu.Unlock()
	
	// Update supervisor config
	oldCfg := s.supervisor.cfg
	s.supervisor.cfg = newCfg
	
	// Handle program changes
	s.handleConfigChanges(oldCfg, newCfg)
	
	fmt.Println("Configuration reloaded successfully.")
}

func (s *Shell) handleConfigChanges(oldCfg, newCfg *Config) {
	// Stop programs that are no longer in config
	for name := range oldCfg.Programs {
		if _, exists := newCfg.Programs[name]; !exists {
			fmt.Printf("Stopping removed program: %s\n", name)
			if processes, running := s.supervisor.programs[name]; running {
				oldProgram := oldCfg.Programs[name]
				for _, proc := range processes {
					if proc != nil {
						s.supervisor.StopProcess(proc, &oldProgram)
					}
				}
				delete(s.supervisor.programs, name)
			}
		}
	}
	
	// Start new programs or restart changed ones
	for name, newProgram := range newCfg.Programs {
		oldProgram, existed := oldCfg.Programs[name]
		
		if !existed {
			// New program
			fmt.Printf("Starting new program: %s\n", name)
			if newProgram.Autostart {
				s.supervisor.StartProgram(name, &newProgram)
			}
		} else if !programsEqual(oldProgram, newProgram) {
			// Program changed
			fmt.Printf("Restarting changed program: %s\n", name)
			if processes, running := s.supervisor.programs[name]; running {
				for _, proc := range processes {
					if proc != nil {
						s.supervisor.StopProcess(proc, &oldProgram)
					}
				}
			}
			delete(s.supervisor.programs, name)
			
			if newProgram.Autostart {
				s.supervisor.StartProgram(name, &newProgram)
			}
		}
	}
}

func programsEqual(p1, p2 Program) bool {
	return p1.Command == p2.Command &&
		p1.NumProcs == p2.NumProcs &&
		p1.Directory == p2.Directory &&
		p1.Autostart == p2.Autostart &&
		p1.Autorestart == p2.Autorestart &&
		p1.StopSignal == p2.StopSignal
}

func (s *Shell) quit() {
	fmt.Println("Shutting down taskmaster...")
	s.running = false
	
	// Stop all programs
	s.supervisor.mu.Lock()
	defer s.supervisor.mu.Unlock()
	
	for name, processes := range s.supervisor.programs {
		program := s.supervisor.cfg.Programs[name]
		fmt.Printf("Stopping program: %s\n", name)
		for _, proc := range processes {
			if proc != nil {
				s.supervisor.StopProcess(proc, &program)
			}
		}
	}
	
	// Cancel supervisor context
	s.supervisor.cancel()
	
	fmt.Println("Goodbye!")
	os.Exit(0)
}
