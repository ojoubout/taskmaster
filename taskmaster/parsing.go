package taskmaster

import (
	"fmt"
	"os"
	"gopkg.in/yaml.v3"
	"errors"
)

type ExitCodes []int

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

type Config struct {
	Programs map[string]Program 
}

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
	if p.Autorestart != "" && p.Autorestart != "unexpected" && p.Autorestart != "always" && p.Autorestart != "never" {
		return fmt.Errorf("program '%s': autorestart must be one of 'unexpected', 'always', 'never'", name)
	}
	if len(p.ExitCodes) == 0 {
		return fmt.Errorf("program '%s': exitcodes must be specified", name)
	}
	return nil
}
