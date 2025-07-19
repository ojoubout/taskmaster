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
	taskmaster.SigNotifier(cfg)

	// programs := make(map[string]map[int]*exec.Cmd)

	supervisor := taskmaster.NewSupervisor(cfg)

	taskmaster.RunInitialState(supervisor)

	fmt.Println("Taskmaster is running. PID:", os.Getpid())
	select {}
}
