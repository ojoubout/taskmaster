package taskmaster

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

type Shell struct {
	supervisor *Supervisor
	configFile string
	running    bool
}

func NewShell(supervisor *Supervisor, configFile string) *Shell {
	return &Shell{
		supervisor: supervisor,
		configFile: configFile,
		running:    true,
	}
}

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

func (s *Shell) Start() {
	fmt.Println("Taskmaster control shell started. Type 'help' for commands.")

	scanner := bufio.NewScanner(os.Stdin)

	for s.running {
		fmt.Print("taskmaster> ") // Show prompt

		// Read next line of input from user
		if !scanner.Scan() {
			// EOF (Ctrl+D) or error - trigger graceful shutdown
			fmt.Println() // Print newline for clean output
			s.quit()
			return
		}

		command := strings.TrimSpace(scanner.Text())
		if command == "" {
			continue
		}

		s.processCommand(command)
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("Error reading input: %v\n", err)
		s.quit()
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

func (s *Shell) showStatus(args []string) {
	if len(args) == 0 {
		fmt.Printf("%-20s %-12s %-8s %-12s %-8s %-s\n", "PROGRAM", "STATE", "PID", "UPTIME", "RETRIES", "DESCRIPTION")
		fmt.Println(strings.Repeat("-", 80))

		allProcesses := s.supervisor.GetAllProcessStates()

		trackedProcesses := make(map[string]*ProcessInfo)
		for _, info := range allProcesses {
			key := fmt.Sprintf("%s:%d", info.Name, info.InstanceID)
			trackedProcesses[key] = info
		}

		for name, program := range s.supervisor.cfg.Programs {
			for i := 0; i < program.NumProcs; i++ {
				programName := name
				if program.NumProcs > 1 {
					programName = fmt.Sprintf("%s_%02d", name, i)
				}

				key := fmt.Sprintf("%s:%d", name, i)
				if info, exists := trackedProcesses[key]; exists {
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
				} else {
					fmt.Printf("%-20s %-12s %-8s %-12s %-8s %-s\n",
						programName, "STOPPED", "-", "-", "-", "Not started")
				}
			}
		}
	} else {
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

		trackedMap := make(map[int]*ProcessInfo)
		for _, info := range processes {
			trackedMap[info.InstanceID] = info
		}

		for i := 0; i < program.NumProcs; i++ {
			if info, exists := trackedMap[i]; exists {
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
			} else {
				fmt.Printf("  Instance %d:\n", i)
				fmt.Printf("    State: STOPPED\n")
				fmt.Printf("    Description: Not started\n")
				fmt.Println()
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

	s.supervisor.mu.RLock()
	processes, running := s.supervisor.programs[programName]
	s.supervisor.mu.RUnlock()

	if running && len(processes) > 0 {
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

	s.supervisor.mu.RLock()
	processes, running := s.supervisor.programs[programName]
	s.supervisor.mu.RUnlock()

	if !running || len(processes) == 0 {
		fmt.Printf("Program '%s' is not running.\n", programName)
		return
	}

	fmt.Printf("Stopping program '%s'...\n", programName)

	err := s.supervisor.StopProgram(programName, &program)
	if err != nil {
		fmt.Printf("Error stopping program '%s': %v\n", programName, err)
		return
	}

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

	if s.supervisor.logger != nil {
		s.supervisor.logger.LogProgramRestart(programName, "manual restart")
	}

	s.stopProgram(args)

	fmt.Print("Waiting for cleanup...")
	for i := 0; i < 3; i++ {
		fmt.Print(".")
	}
	fmt.Println()

	s.startProgram(args)
}

func (s *Shell) quit() {
	fmt.Println("Shutting down taskmaster...")
	s.running = false // Stop the shell loop

	s.supervisor.Shutdown()

	fmt.Println("Goodbye!")
	os.Exit(0)
}
