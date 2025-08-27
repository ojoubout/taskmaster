// Package taskmaster provides process supervision functionality
// This file handles configuration parsing from YAML files
package taskmaster

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ExitCodes handles both single integers and arrays of integers in YAML
// This is needed because YAML config might specify exitcodes as either:
// exitcodes: 0        (single value)
// exitcodes: [0, 1]   (array of values)
type ExitCodes []int

// UnmarshalYAML is a custom YAML unmarshaling function for ExitCodes
func (e *ExitCodes) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.SequenceNode:
		var codes []int
		if err := value.Decode(&codes); err != nil {
			return err
		}
		*e = codes
	case yaml.ScalarNode:
		var code int
		if err := value.Decode(&code); err != nil {
			return err
		}
		*e = []int{code}
	default:
		return errors.New("exitcodes must be int or list of ints")
	}
	return nil
}

// Program represents a single program configuration from the YAML file
type Program struct {
	Command      string            `yaml:"command"`
	NumProcs     int               `yaml:"numprocs"`
	Umask        int               `yaml:"umask"`
	Directory    string            `yaml:"directory"`
	Autostart    bool              `yaml:"autostart"`
	Autorestart  string            `yaml:"autorestart"`
	ExitCodes    ExitCodes         `yaml:"exitcodes"`
	StartRetries int               `yaml:"startretries"`
	StartSecs    int               `yaml:"startsecs"`
	StopSignal   string            `yaml:"stopsignal"`
	StopWaitSecs int               `yaml:"stopwaitsecs"`
	Stdout       string            `yaml:"stdout"`
	Stderr       string            `yaml:"stderr"`
	Env          map[string]string `yaml:"env"`
}

// Config represents the entire configuration file
type Config struct {
	Programs map[string]Program
}

// LoadConfig reads and parses a YAML configuration file
func LoadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var cfg Config
	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, err
	}

	for name, prog := range cfg.Programs {
		if err := validateProgram(name, prog); err != nil {
			return nil, err
		}
	}

	return &cfg, nil
}

// validateProgram checks that a program configuration has all required fields and valid values
func validateProgram(name string, p Program) error {
	if p.Command == "" {
		return fmt.Errorf("program '%s': command is required", name)
	}
	if p.NumProcs < 1 {
		return fmt.Errorf("program '%s': numprocs must be >= 1", name)
	}
	if p.Directory == "" {
		return fmt.Errorf("program '%s': directory is required", name)
	}

	if p.Autorestart != "" && p.Autorestart != "unexpected" &&
		p.Autorestart != "always" && p.Autorestart != "never" {
		return fmt.Errorf("program '%s': autorestart must be one of 'unexpected', 'always', 'never'", name)
	}

	if len(p.ExitCodes) == 0 {
		return fmt.Errorf("program '%s': exitcodes must be specified", name)
	}

	return nil
}
