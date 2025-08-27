// Demo program to showcase the logging system functionality
package main

import (
	"fmt"
	"os"
	"taskmaster/taskmaster"
)

func main() {
	fmt.Println("=== Taskmaster Logging System Demo ===")
	
	// Create a logger
	logger, err := taskmaster.NewLogger("demo.log", taskmaster.LogLevelInfo)
	if err != nil {
		fmt.Printf("Error creating logger: %v\n", err)
		return
	}
	defer logger.Close()
	
	// Demonstrate different log levels
	fmt.Println("1. Testing different log levels...")
	logger.Info("This is an info message")
	logger.Warn("This is a warning message")
	logger.Error("This is an error message")
	logger.Debug("This debug message won't appear (log level is INFO)")
	
	// Demonstrate program lifecycle logging
	fmt.Println("2. Testing program lifecycle logging...")
	logger.LogTaskmasterStart(os.Getpid(), "demo.conf")
	logger.LogProgramStart("test_program", 12345, "/bin/sleep 10")
	logger.LogProgramStop("test_program", 12345, "TERM")
	logger.LogProgramExit("test_program", 12345, 0, true)
	logger.LogProgramRestart("test_program", "configuration change")
	
	// Demonstrate configuration reload logging
	fmt.Println("3. Testing configuration reload logging...")
	logger.LogConfigReload(true, "")
	logger.LogConfigReload(false, "file not found")
	
	// Demonstrate error logging
	fmt.Println("4. Testing error logging...")
	logger.LogStartupFailure("failing_program", 1, 3, fmt.Errorf("command not found"))
	logger.LogError("file operation", fmt.Errorf("permission denied"))
	
	// Demonstrate shutdown logging
	fmt.Println("5. Testing shutdown logging...")
	logger.LogTaskmasterStop()
	
	fmt.Println("\nDemo completed! Check 'demo.log' file for the logged events.")
	fmt.Println("Log format: [TIMESTAMP] [LEVEL] MESSAGE")
}