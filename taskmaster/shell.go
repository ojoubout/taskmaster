// Package taskmaster provides the interactive control shell
// This file implements a command-line interface similar to supervisorctl
package taskmaster

import (
	"bufio"   // For buffered I/O operations (reading user input line by line)
	"fmt"     // For formatted I/O operations (printing to console)
	"os"      // For operating system interface (os.Stdin, os.Exit)
	"strings" // For string manipulation (splitting, trimming, etc.)
	"time"    // For time duration formatting
)

// Shell represents the interactive command-line interface
// It provides commands to control the supervisor and its programs
type Shell struct {
	supervisor *Supervisor // Reference to the supervisor that manages processes
	configFile string      // Path to config file for reloads
	running    bool        // Flag to control the main shell loop
}

// NewShell creates a new Shell instance with the given supervisor
// This is a constructor function following Go conventions
func NewShell(supervisor *Supervisor, configFile string) *Shell {
	return &Shell{
		supervisor: supervisor,
		configFile: configFile,
		running:    true, // Start in running state
	}
}

// formatDuration formats a duration into a human-readable string
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return "0s"
	}

	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %02d:%02d:%02d", days, hours, minutes, seconds)
	} else if hours > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
	} else if minutes > 0 {
		return fmt.Sprintf("%02d:%02d", minutes, seconds)
	} else {
		return fmt.Sprintf("%ds", seconds)
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
		fmt.Print("taskmaster> ") // Show prompt

		// Read next line of input from user
		if !scanner.Scan() {
			break // Exit if there's an error or EOF (Ctrl+D)
		}

		// Get the command text and remove leading/trailing whitespace
		command := strings.TrimSpace(scanner.Text())
		if command == "" {
			continue // Skip empty lines
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
	parts := strings.Fields(command) // Fields splits on any whitespace
	if len(parts) == 0 {
		return
	}

	// First part is the command, rest are arguments
	cmd := strings.ToLower(parts[0]) // Convert to lowercase for case-insensitive matching
	args := parts[1:]                // Slice from index 1 to end (excludes first element)

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
		if err := s.supervisor.ReloadConfig(s.configFile); err != nil {
			fmt.Printf("Reload failed: %v\n", err)
		} else {
			fmt.Println("Configuration reloaded successfully.")
		}
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
	if len(args) == 0 {
		// Show status of all programs in a supervisor-like table format
		fmt.Printf("%-20s %-12s %-8s %-12s %-8s %-s\n", "PROGRAM", "STATE", "PID", "UPTIME", "RETRIES", "DESCRIPTION")
		fmt.Println(strings.Repeat("-", 80)) // Print separator line

		// Get all process states from the state tracker
		allProcesses := s.supervisor.GetAllProcessStates()

		// If no processes are tracked, show configured programs as STOPPED
		if len(allProcesses) == 0 {
			for name, program := range s.supervisor.cfg.Programs {
				for i := 0; i < program.NumProcs; i++ {
					programName := name
					if program.NumProcs > 1 {
						programName = fmt.Sprintf("%s:%d", name, i)
					}
					fmt.Printf("%-20s %-12s %-8s %-12s %-8s %-s\n",
						programName, "STOPPED", "-", "-", "-", "Not started")
				}
			}
			return
		}

		// Display tracked processes
		for _, info := range allProcesses {
			programName := info.Name
			if info.InstanceID > 0 {
				programName = fmt.Sprintf("%s:%d", info.Name, info.InstanceID)
			}

			pid := "-"
			if info.PID > 0 {
				pid = fmt.Sprintf("%d", info.PID)
			}

			uptime := "-"
			if info.State == RUNNING && info.Uptime > 0 {
				uptime = formatDuration(info.Uptime)
			}

			fmt.Printf("%-20s %-12s %-8s %-12s %-8d %s\n",
				programName, info.State.String(), pid, uptime, info.Retries, info.Description)
		}
	} else {
		// Show detailed status of specific program
		programName := args[0]
		program, exists := s.supervisor.cfg.Programs[programName]
		if !exists {
			fmt.Printf("Program '%s' not found in configuration.\n", programName)
			return
		}

		// Get all instances of this program
		processes := s.supervisor.GetProgramStates(programName)

		fmt.Printf("Program: %s\n", programName)
		fmt.Printf("Command: %s\n", program.Command)
		fmt.Printf("NumProcs: %d\n", program.NumProcs)
		fmt.Printf("Autostart: %t\n", program.Autostart)
		fmt.Printf("Autorestart: %s\n", program.Autorestart)
		fmt.Printf("StartRetries: %d\n", program.StartRetries)
		fmt.Printf("StartSecs: %d\n", program.StartSecs)
		fmt.Printf("StopSignal: %s\n", program.StopSignal)
		fmt.Printf("StopWaitSecs: %d\n", program.StopWaitSecs)
		fmt.Printf("Expected Exit Codes: %v\n", program.ExitCodes)
		fmt.Printf("\nInstances:\n")

		if len(processes) == 0 {
			fmt.Printf("  No instances tracked (program may not have been started)\n")
		} else {
			for _, info := range processes {
				fmt.Printf("  Instance %d:\n", info.InstanceID)
				fmt.Printf("    State: %s\n", info.State.String())
				fmt.Printf("    Description: %s\n", info.Description)
				if info.PID > 0 {
					fmt.Printf("    PID: %d\n", info.PID)
				}
				if info.State == RUNNING && info.Uptime > 0 {
					fmt.Printf("    Uptime: %s\n", formatDuration(info.Uptime))
				}
				if !info.StartTime.IsZero() {
					fmt.Printf("    Start Time: %s\n", info.StartTime.Format("2006-01-02 15:04:05"))
				}
				if !info.StopTime.IsZero() {
					fmt.Printf("    Stop Time: %s\n", info.StopTime.Format("2006-01-02 15:04:05"))
				}
				fmt.Printf("    Retries: %d/%d\n", info.Retries, info.MaxRetries)
				if info.ExitStatus != 0 {
					fmt.Printf("    Last Exit Status: %d (expected: %v)\n", info.ExitStatus, info.ExpectedExit)
				}
				fmt.Println()
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

	// Check if already running (without holding lock)
	s.supervisor.mu.RLock()
	processes, running := s.supervisor.programs[programName]
	s.supervisor.mu.RUnlock()

	if running && len(processes) > 0 {
		fmt.Printf("Program '%s' is already running.\n", programName)
		return
	}

	fmt.Printf("Starting program '%s'...\n", programName)
	// StartProgram handles its own locking
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

	// Check if program is running (without holding lock)
	s.supervisor.mu.RLock()
	processes, running := s.supervisor.programs[programName]
	s.supervisor.mu.RUnlock()

	if !running || len(processes) == 0 {
		fmt.Printf("Program '%s' is not running.\n", programName)
		return
	}

	fmt.Printf("Stopping program '%s'...\n", programName)

	// StopProgram handles its own locking
	err := s.supervisor.StopProgram(programName, &program)
	if err != nil {
		fmt.Printf("Error stopping program '%s': %v\n", programName, err)
		return
	}

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

	// Log the restart event
	if s.supervisor.logger != nil {
		s.supervisor.logger.LogProgramRestart(programName, "manual restart")
	}

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

// (Reload logic removed from shell; delegated to Supervisor.ReloadConfig)

// quit shuts down the taskmaster system gracefully
// This stops all running programs and exits the application
func (s *Shell) quit() {
	fmt.Println("Shutting down taskmaster...")
	s.running = false // Stop the shell loop

	// Log taskmaster shutdown
	if s.supervisor.logger != nil {
		s.supervisor.logger.LogTaskmasterStop()
	}

	// Stop all programs before exiting
	s.supervisor.mu.Lock()
	defer s.supervisor.mu.Unlock()

	// Iterate through all running programs and stop them
	for name, processes := range s.supervisor.programs {
		program := s.supervisor.cfg.Programs[name]
		fmt.Printf("Stopping program: %s\n", name)
		for _, proc := range processes {
			if proc != nil {
				s.supervisor.StopProcess(proc, name, &program)
			}
		}
	}

	// Cancel supervisor context to stop monitoring goroutines
	s.supervisor.cancel()

	// Close the logger
	if s.supervisor.logger != nil {
		s.supervisor.logger.Close()
	}

	fmt.Println("Goodbye!")
	os.Exit(0) // Exit the entire application
}
