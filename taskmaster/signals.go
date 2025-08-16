// Package taskmaster provides signal handling for configuration reloading and graceful shutdown
// This file implements Unix signal handlers for taskmaster daemon operations
package taskmaster

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// SetupSignalHandling configures SIGHUP (reload) and SIGINT/SIGTERM (graceful shutdown)
func SetupSignalHandling(spv *Supervisor, configFile string) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		for sig := range sigs {
			switch sig {
			case syscall.SIGHUP:
				fmt.Println("[taskmaster] Received SIGHUP: reloading configuration")
				if err := spv.ReloadConfig(configFile); err != nil {
					fmt.Fprintf(os.Stderr, "[taskmaster] Reload failed: %v\n", err)
				} else {
					fmt.Println("[taskmaster] Configuration reloaded (SIGHUP)")
				}
			case syscall.SIGINT, syscall.SIGTERM:
				fmt.Printf("[taskmaster] Received %s: initiating shutdown\n", sig.String())
				// Perform graceful shutdown then exit
				spv.Shutdown()
				os.Exit(0)
			}
		}
	}()
}
