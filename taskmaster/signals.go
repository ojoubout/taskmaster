// Package taskmaster provides signal handling for configuration reloading and graceful shutdown
// This file implements Unix signal handlers for taskmaster daemon operations
package taskmaster

import (
	"fmt"        // For formatted I/O operations
	"os"         // For operating system interface
	"os/signal"  // For signal handling functionality
	"syscall"    // For system call constants (signal types)
)

// onSIGHUP handles the SIGHUP signal which typically means "reload configuration"
// SIGHUP (Signal Hang Up) is traditionally sent to daemons to tell them to reload config
func onSIGHUP(cfg *Config) {
	// Attempt to load new configuration from file
	new_cfg, err := LoadConfig("taskmaster.conf")
	if err != nil {
		// If reload fails, print error but don't crash - keep running with old config
		fmt.Fprintln(os.Stderr, "Error re-loading config:", err)
		return
	}
	
	// Replace current configuration with new configuration
	// The * operator dereferences the pointer to access the actual Config struct
	*cfg = *new_cfg
	fmt.Println("Configuration reloaded successfully via SIGHUP")
}

// onSIGINT handles the SIGINT signal (Ctrl+C) for graceful shutdown
// SIGINT (Signal Interrupt) is sent when user presses Ctrl+C
func onSIGINT() {
	// \b\b moves cursor back to overwrite "^C" that terminal shows when user presses Ctrl+C
	fmt.Println("\b\bGraceful shutdown...")
	// TODO: This should properly stop all running processes before exiting
	// Currently this just exits immediately, which could leave orphaned processes
	os.Exit(0)
}

// SigNotifier sets up signal handling in a background goroutine
// This function creates a signal handler that runs concurrently with the main program
func SigNotifier(cfg *Config) {
	// Step 1: Create a channel to receive signals
	// Buffered channel with capacity 1 prevents blocking if signals arrive rapidly
	sigs := make(chan os.Signal, 1)
	
	// Step 2: Register which signals we want to handle
	// signal.Notify tells Go to send these signals to our channel instead of default handling
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGINT)
	
	// Step 3: Start a goroutine to handle signals
	// This runs concurrently so signals can be handled while main program continues
	go func() {
		// Infinite loop to continuously handle signals
		for {
			// Block until a signal is received
			// The <- operator receives from the channel
			sig := <-sigs
			
			// Handle different signal types
			switch sig {
			case syscall.SIGHUP:
				// Handle configuration reload
				onSIGHUP(cfg)
			case syscall.SIGINT:
				// Handle graceful shutdown
				onSIGINT()
			}
		}
	}()
	// Note: This function returns immediately after starting the goroutine
	// The signal handling continues running in the background
}
