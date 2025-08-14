package main

import (
	"fmt"
	"os"
	"taskmaster/taskmaster"
)

func main() {
	cfg, err := taskmaster.LoadConfig("taskmaster.conf")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error loading config:", err)
		os.Exit(1)
	}

	fmt.Println("Config loaded and validated successfully!")
	for name, prog := range cfg.Programs {
		fmt.Printf("Program: %s, Command: %s, NumProcs: %d, ExitCodes: %v\n", name, prog.Command, prog.NumProcs, prog.ExitCodes)
	}
	
	// Start supervisor
	supervisor := taskmaster.NewSupervisor(cfg)
	taskmaster.RunInitialState(supervisor)
	
	// Set up signal handling for SIGHUP reload
	go taskmaster.SigNotifier(cfg)

	fmt.Println("Taskmaster is running. PID:", os.Getpid())
	
	// Start control shell
	shell := taskmaster.NewShell(supervisor)
	shell.Start()
}
