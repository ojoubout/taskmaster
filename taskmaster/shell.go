// Package taskmaster provides the interactive control shell
// This file implements a command-line interface similar to supervisorctl
package taskmaster

import (
	"bufio"    // For buffered I/O operations (reading user input line by line)
	"fmt"      // For formatted I/O operations (printing to console)
	"os"       // For operating system interface (os.Stdin, os.Exit)
	"strings"  // For string manipulation (splitting, trimming, etc.)
	"sync"     // For synchronization primitives (mutex for thread safety)
	"syscall"  // For system calls (checking if process is running)
)

// Shell represents the interactive command-line interface
// It provides commands to control the supervisor and its programs
type Shell struct {
	supervisor *Supervisor   // Reference to the supervisor that manages processes
	running    bool          // Flag to control the main shell loop
	mu         sync.RWMutex  // Read-Write mutex for thread-safe access to running flag
}

// NewShell creates a new Shell instance with the given supervisor
// This is a constructor function following Go conventions
func NewShell(supervisor *Supervisor) *Shell {
	return &Shell{
		supervisor: supervisor,
		running:    true,  // Start in running state
	}
}

// Start begins the interactive shell loop
// This function blocks until the user types 'quit' or 'exit'
func (s *Shell) Start() {
	fmt.Println("Taskmaster control shell started. Type 'help' for commands.")
	
	// Create a scanner to read user input line by line
	scanner := bufio.NewScanner(os.Stdin)
	
	// Main shell loop - continues until s.running becomes false
	for s.running {
		fmt.Print("taskmaster> ")  // Show prompt
		
		// Read next line of input from user
		if !scanner.Scan() {
			break  // Exit if there's an error or EOF (Ctrl+D)
		}
		
		// Get the command text and remove leading/trailing whitespace
		command := strings.TrimSpace(scanner.Text())
		if command == "" {
			continue  // Skip empty lines
		}
		
		// Process the command
		s.processCommand(command)
	}
	
	// Check if there was an error reading input
	if err := scanner.Err(); err != nil {
		fmt.Printf("Error reading input: %v\n", err)
	}
}

// processCommand parses and executes a user command
// Commands are space-separated: "start program_name" becomes ["start", "program_name"]
func (s *Shell) processCommand(command string) {
	// Split command into parts (command and arguments)
	parts := strings.Fields(command)  // Fields splits on any whitespace
	if len(parts) == 0 {
		return
	}
	
	// First part is the command, rest are arguments
	cmd := strings.ToLower(parts[0])  // Convert to lowercase for case-insensitive matching
	args := parts[1:]                 // Slice from index 1 to end (excludes first element)
	
	// Route to appropriate handler based on command
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

// showHelp displays all available commands and their usage
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

// showStatus displays the status of programs
// If no arguments are provided, shows all programs
// If a program name is provided, shows detailed info for that program
func (s *Shell) showStatus(args []string) {
	// Lock supervisor for reading to ensure thread safety
	s.supervisor.mu.RLock()
	defer s.supervisor.mu.RUnlock()  // Unlock when function returns
	
	if len(args) == 0 {
		// Show status of all programs in a table format
		fmt.Printf("%-15s %-10s %-8s %-s\n", "PROGRAM", "STATUS", "PID", "COMMAND")
		fmt.Println(strings.Repeat("-", 60))  // Print separator line
		
		// Iterate through all configured programs
		for name, program := range s.supervisor.cfg.Programs {
			processes, exists := s.supervisor.programs[name]
			
			// If program has no running processes, show as STOPPED
			if !exists || len(processes) == 0 {
				fmt.Printf("%-15s %-10s %-8s %-s\n", name, "STOPPED", "-", program.Command)
				continue
			}
			
			// Show each running process (for programs with numprocs > 1)
			for i, proc := range processes {
				status := "UNKNOWN"
				pid := "-"
				
				if proc != nil && proc.Process != nil {
					pid = fmt.Sprintf("%d", proc.Process.Pid)
					
					// Check if process is still running using signal 0
					// Signal 0 doesn't actually send a signal, just checks if process exists
					if proc.ProcessState == nil {
						// Process hasn't exited yet, verify it's actually running
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
				
				// For multiple processes, append index to name (program_0, program_1, etc.)
				programName := name
				if i > 0 {
					programName = fmt.Sprintf("%s_%d", name, i)
				}
				
				fmt.Printf("%-15s %-10s %-8s %-s\n", programName, status, pid, program.Command)
			}
		}
	} else {
		// Show detailed status of specific program
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

// startProgram starts a specific program by name
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
	
	// Lock supervisor for writing since we'll modify the programs map
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

// stopProgram stops a specific program by name
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
	// Stop each running process
	for _, proc := range processes {
		if proc != nil {
			s.supervisor.StopProcess(proc, &program)
		}
	}
	
	// Remove from running programs map
	delete(s.supervisor.programs, programName)
	fmt.Printf("Program '%s' stopped.\n", programName)
}

// restartProgram stops then starts a program (convenience function)
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
	
	// Small delay to ensure cleanup (visual feedback)
	fmt.Print("Waiting for cleanup...")
	for i := 0; i < 3; i++ {
		fmt.Print(".")
		// Note: time.Sleep is commented out to avoid blocking too long
		// In a real implementation, you might want a small delay here
	}
	fmt.Println()
	
	// Start again
	s.startProgram(args)
}

// reloadConfig reloads the configuration file and applies changes
// This implements hot-reloading of configuration without restarting taskmaster
func (s *Shell) reloadConfig() {
	fmt.Println("Reloading configuration...")
	
	// Load new config from file
	newCfg, err := LoadConfig("taskmaster.conf")
	if err != nil {
		fmt.Printf("Error reloading config: %v\n", err)
		return
	}
	
	s.supervisor.mu.Lock()
	defer s.supervisor.mu.Unlock()
	
	// Keep reference to old config for comparison
	oldCfg := s.supervisor.cfg
	s.supervisor.cfg = newCfg
	
	// Handle differences between old and new config
	s.handleConfigChanges(oldCfg, newCfg)
	
	fmt.Println("Configuration reloaded successfully.")
}

// handleConfigChanges compares old and new configurations and applies changes
// This stops removed programs, starts new programs, and restarts modified programs
func (s *Shell) handleConfigChanges(oldCfg, newCfg *Config) {
	// Step 1: Stop programs that are no longer in the new configuration
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
	
	// Step 2: Handle new programs and modified programs
	for name, newProgram := range newCfg.Programs {
		oldProgram, existed := oldCfg.Programs[name]
		
		if !existed {
			// This is a completely new program
			fmt.Printf("Starting new program: %s\n", name)
			if newProgram.Autostart {
				s.supervisor.StartProgram(name, &newProgram)
			}
		} else if !programsEqual(oldProgram, newProgram) {
			// Program exists but has been modified - restart it
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
		// If program exists and hasn't changed, leave it alone
	}
}

// programsEqual compares two Program configurations to see if they're identical
// This determines whether a program needs to be restarted after config reload
func programsEqual(p1, p2 Program) bool {
	// Compare the most important fields that would require a restart
	return p1.Command == p2.Command &&
		p1.NumProcs == p2.NumProcs &&
		p1.Directory == p2.Directory &&
		p1.Autostart == p2.Autostart &&
		p1.Autorestart == p2.Autorestart &&
		p1.StopSignal == p2.StopSignal
	// Note: We don't compare all fields (like stdout/stderr paths) 
	// since some changes might not require a restart
}

// quit shuts down the taskmaster system gracefully
// This stops all running programs and exits the application
func (s *Shell) quit() {
	fmt.Println("Shutting down taskmaster...")
	s.running = false  // Stop the shell loop
	
	// Stop all programs before exiting
	s.supervisor.mu.Lock()
	defer s.supervisor.mu.Unlock()
	
	// Iterate through all running programs and stop them
	for name, processes := range s.supervisor.programs {
		program := s.supervisor.cfg.Programs[name]
		fmt.Printf("Stopping program: %s\n", name)
		for _, proc := range processes {
			if proc != nil {
				s.supervisor.StopProcess(proc, &program)
			}
		}
	}
	
	// Cancel supervisor context to stop monitoring goroutines
	s.supervisor.cancel()
	
	fmt.Println("Goodbye!")
	os.Exit(0)  // Exit the entire application
}
