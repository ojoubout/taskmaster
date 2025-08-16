// Package main is the entry point for the taskmaster application
// Taskmaster is a job control daemon (process supervisor) similar to supervisord
package main

import (
	"fmt"                   // For formatted I/O operations (printing)
	"os"                    // For operating system interface (file operations, exit codes)
	"taskmaster/taskmaster" // Import our custom taskmaster package
)

func main() {
	// Step 1: Parse command line arguments for config file
	configFile := "taskmaster.conf" // Default config file
	if len(os.Args) > 1 {
		configFile = os.Args[1] // Use first argument as config file if provided
	}

	// Step 2: Load and parse configuration file
	// LoadConfig reads the specified config file (YAML format) and validates all settings
	cfg, err := taskmaster.LoadConfig(configFile)
	if err != nil {
		// If config loading fails, print error to stderr and exit with status 1
		fmt.Fprintln(os.Stderr, "Error loading config:", err)
		os.Exit(1)
	}

	// Step 3: Initialize the logging system
	// Create a logger that writes to taskmaster.log with INFO level
	logger, err := taskmaster.NewLogger("taskmaster.log", taskmaster.LogLevelInfo)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error creating logger:", err)
		os.Exit(1)
	}
	defer logger.Close() // Ensure logger is closed when main exits

	// Step 4: Log taskmaster startup
	logger.LogTaskmasterStart(os.Getpid(), configFile)

	// Step 5: Display loaded configuration for verification
	// This shows what programs were loaded and their basic settings
	fmt.Println("Config loaded and validated successfully!")
	for name, prog := range cfg.Programs {
		// Range iterates over the Programs map: name is the key, prog is the Program struct
		fmt.Printf("Program: %s, Command: %s, NumProcs: %d, ExitCodes: %v\n",
			name, prog.Command, prog.NumProcs, prog.ExitCodes)
	}

	// Step 5: Create the supervisor instance with logger
	// The supervisor is the core component that manages all child processes
	supervisor := taskmaster.NewSupervisor(cfg, logger)

	// Step 6: Start initial programs that have autostart=true
	// This goes through all programs and starts the ones configured to auto-start
	taskmaster.RunInitialState(supervisor)

	// Step 7: Set up signal handling for configuration reload
	// SIGHUP signal is commonly used to tell daemons to reload their configuration
	go taskmaster.SigNotifier(cfg)

	// Step 8: Display process information
	// Shows that taskmaster is running and its Process ID (useful for sending signals)
	fmt.Println("Taskmaster is running. PID:", os.Getpid())

	// Step 9: Start the interactive control shell
	// This provides a command-line interface for managing processes (start, stop, status, etc.)
	shell := taskmaster.NewShell(supervisor)
	shell.Start() // This blocks and runs the interactive shell until user quits
}
