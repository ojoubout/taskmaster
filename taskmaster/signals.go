// Package taskmaster provides signal handling for configuration reloading and graceful shutdown
// This file implements Unix signal handlers for taskmaster daemon operations
package taskmaster

import (
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
				if spv.logger != nil {
					spv.logger.Info("Received SIGHUP: reloading configuration")
				}
				if err := spv.ReloadConfig(configFile); err != nil {
					if spv.logger != nil {
						spv.logger.Error("Configuration reload failed: %v", err)
					}
				} else {
					if spv.logger != nil {
						spv.logger.Info("Configuration reloaded successfully (SIGHUP)")
					}
				}
			case syscall.SIGINT, syscall.SIGTERM:
				if spv.logger != nil {
					spv.logger.Info("Received %s: initiating shutdown", sig.String())
				}
				// Perform graceful shutdown then exit
				spv.Shutdown()
				os.Exit(0)
			}
		}
	}()
}
