// Package taskmaster provides process supervision functionality
// This file handles configuration parsing from YAML files
package taskmaster

import (
	"fmt"                // For formatted string operations and error messages
	"os"                 // For file operations
	"gopkg.in/yaml.v3"   // Third-party YAML parsing library
	"errors"             // For creating custom error messages
)

// ExitCodes is a custom type that can handle both single integers and arrays of integers
// This is needed because YAML config might specify exitcodes as either:
// exitcodes: 0        (single value)
// exitcodes: [0, 1]   (array of values)
type ExitCodes []int

// UnmarshalYAML is a custom YAML unmarshaling function for ExitCodes
// This tells the YAML parser how to convert YAML data into our ExitCodes type
func (e *ExitCodes) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.SequenceNode: // If it's an array like [0, 1, 2]
		var codes []int
		if err := value.Decode(&codes); err != nil {
			return err
		}
		*e = codes // Set the value by dereferencing the pointer
	case yaml.ScalarNode: // If it's a single value like 0
		var code int
		if err := value.Decode(&code); err != nil {
			return err
		}
		*e = []int{code} // Convert single value to slice with one element
	default:
		return errors.New("exitcodes must be int or list of ints")
	}
	return nil
}

// Program represents a single program configuration from the YAML file
// The yaml:"fieldname" tags tell the YAML parser which YAML field maps to which struct field
type Program struct {
	Command      string            `yaml:"command"`      // Command to execute (e.g., "/bin/sleep 10")
	NumProcs     int               `yaml:"numprocs"`     // Number of processes to run (default 1)
	Umask        int               `yaml:"umask"`        // File permission mask for created files
	Directory    string            `yaml:"directory"`    // Working directory for the process
	Autostart    bool              `yaml:"autostart"`    // Start automatically when taskmaster starts
	Autorestart  string            `yaml:"autorestart"`  // When to restart: "always", "never", "unexpected"
	ExitCodes    ExitCodes         `yaml:"exitcodes"`    // Which exit codes are considered "successful"
	StartRetries int               `yaml:"startretries"` // How many times to retry starting if it fails
	StartSecs    int               `yaml:"startsecs"`    // How long process must run to be considered "started"
	StopSignal   string            `yaml:"stopsignal"`   // Signal to send when stopping (TERM, KILL, etc.)
	StopWaitSecs int               `yaml:"stopwaitsecs"` // How long to wait after stop signal before SIGKILL
	Stdout       string            `yaml:"stdout"`       // File path for stdout redirection
	Stderr       string            `yaml:"stderr"`       // File path for stderr redirection
	Env          map[string]string `yaml:"env"`          // Environment variables to set
}

// Config represents the entire configuration file
// The YAML structure is: programs: { program_name: Program, ... }
type Config struct {
	Programs map[string]Program  // Map of program name to Program configuration
}

// LoadConfig reads and parses a YAML configuration file
func LoadConfig(path string) (*Config, error) {
	// Step 1: Open the configuration file
	file, err := os.Open(path)
	if err != nil {
		return nil, err // Return nil config and the error
	}
	defer file.Close() // Ensure file is closed when function returns

	// Step 2: Parse YAML content into Config struct
	var cfg Config
	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, err
	}
	
	// Step 3: Validate each program configuration
	for name, prog := range cfg.Programs {
		if err := validateProgram(name, prog); err != nil {
			return nil, err
		}
	}
	
	return &cfg, nil // Return pointer to config and no error
}

// validateProgram checks that a program configuration has all required fields
// and that the values are valid
func validateProgram(name string, p Program) error {
	// Check required fields
	if p.Command == "" {
		return fmt.Errorf("program '%s': command is required", name)
	}
	if p.NumProcs < 1 {
		return fmt.Errorf("program '%s': numprocs must be >= 1", name)
	}
	if p.Directory == "" {
		return fmt.Errorf("program '%s': directory is required", name)
	}
	
	// Validate autorestart values (empty string is allowed, means "never")
	if p.Autorestart != "" && p.Autorestart != "unexpected" && 
	   p.Autorestart != "always" && p.Autorestart != "never" {
		return fmt.Errorf("program '%s': autorestart must be one of 'unexpected', 'always', 'never'", name)
	}
	
	// Ensure at least one exit code is specified
	if len(p.ExitCodes) == 0 {
		return fmt.Errorf("program '%s': exitcodes must be specified", name)
	}
	
	return nil // No validation errors
}
