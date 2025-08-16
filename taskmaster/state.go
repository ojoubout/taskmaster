// Package taskmaster provides process state tracking functionality
// This file implements comprehensive process state management similar to supervisor
package taskmaster

import (
	"fmt"
	"sync"
	"time"
)

// ProcessState represents the current state of a process like supervisor
type ProcessState int

const (
	STOPPED  ProcessState = iota // Process is not running
	STARTING                     // Process is starting up
	RUNNING                      // Process is running normally
	BACKOFF                      // Process failed to start and is backing off before retry
	STOPPING                     // Process is in the process of stopping
	EXITED                       // Process exited normally (expected exit code)
	FATAL                        // Process failed to start after max retries or unexpected exit
	UNKNOWN                      // Process state is unknown
)

// String returns the string representation of the process state
func (s ProcessState) String() string {
	switch s {
	case STOPPED:
		return "STOPPED"
	case STARTING:
		return "STARTING"
	case RUNNING:
		return "RUNNING"
	case BACKOFF:
		return "BACKOFF"
	case STOPPING:
		return "STOPPING"
	case EXITED:
		return "EXITED"
	case FATAL:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// ProcessInfo holds detailed information about a process instance
type ProcessInfo struct {
	Name         string        `json:"name"`          // Program name
	InstanceID   int           `json:"instance_id"`   // Instance ID for programs with numprocs > 1
	Group        string        `json:"group"`         // Process group name
	State        ProcessState  `json:"state"`         // Current state
	Description  string        `json:"description"`   // Human-readable state description
	PID          int           `json:"pid"`           // Process ID (0 if not running)
	Uptime       time.Duration `json:"uptime"`        // How long the process has been running
	StartTime    time.Time     `json:"start_time"`    // When the process was started
	StopTime     time.Time     `json:"stop_time"`     // When the process was stopped
	ExitStatus   int           `json:"exit_status"`   // Exit code from last run
	Retries      int           `json:"retries"`       // Current retry count
	MaxRetries   int           `json:"max_retries"`   // Maximum allowed retries
	AutoRestart  string        `json:"auto_restart"`  // Restart policy
	ExpectedExit bool          `json:"expected_exit"` // Whether the exit was expected
	mu           sync.RWMutex  // Mutex for thread-safe access
}

// StateTracker manages process states across all programs and instances
type StateTracker struct {
	processes map[string]*ProcessInfo // Map: "program_name:instance_id" -> ProcessInfo
	mu        sync.RWMutex            // Mutex for thread-safe access to processes map
	logger    *Logger                 // Logger for state change events
}

// NewStateTracker creates a new state tracker instance
func NewStateTracker(logger *Logger) *StateTracker {
	return &StateTracker{
		processes: make(map[string]*ProcessInfo),
		logger:    logger,
	}
}

// getProcessKey creates a unique key for a process instance
func getProcessKey(name string, instanceID int) string {
	return fmt.Sprintf("%s:%d", name, instanceID)
}

// RegisterProcess registers a new process for state tracking
func (st *StateTracker) RegisterProcess(name string, instanceID int, config *Program) {
	st.mu.Lock()
	defer st.mu.Unlock()

	key := getProcessKey(name, instanceID)
	st.processes[key] = &ProcessInfo{
		Name:         name,
		InstanceID:   instanceID,
		Group:        name, // Use program name as group for now
		State:        STOPPED,
		Description:  "Not started",
		PID:          0,
		Retries:      0,
		MaxRetries:   config.StartRetries,
		AutoRestart:  config.Autorestart,
		ExpectedExit: false,
	}

	st.logger.Info("Registered process %s (instance %d) for state tracking", name, instanceID)
}

// UpdateState updates the state of a process instance
func (st *StateTracker) UpdateState(name string, instanceID int, state ProcessState, description string) {
	st.mu.Lock()
	defer st.mu.Unlock()

	key := getProcessKey(name, instanceID)
	process, exists := st.processes[key]
	if !exists {
		st.logger.Warn("Attempted to update state for unregistered process %s:%d", name, instanceID)
		return
	}

	process.mu.Lock()
	defer process.mu.Unlock()

	oldState := process.State
	process.State = state
	process.Description = description

	// Update timestamps and other fields based on state transitions
	now := time.Now()
	switch state {
	case STARTING:
		if oldState == STOPPED || oldState == BACKOFF || oldState == FATAL {
			process.StartTime = now
			process.Retries++
		}
	case RUNNING:
		if oldState == STARTING {
			process.StartTime = now
			st.logger.Info("Process %s:%d successfully started (PID: %d)", name, instanceID, process.PID)
		}
	case STOPPED, EXITED, FATAL:
		process.StopTime = now
		process.PID = 0
	case BACKOFF:
		process.StopTime = now
		process.PID = 0
	}

	st.logger.Info("Process %s:%d state changed: %s -> %s (%s)",
		name, instanceID, oldState.String(), state.String(), description)
}

// SetPID sets the process ID for a running process
func (st *StateTracker) SetPID(name string, instanceID int, pid int) {
	st.mu.RLock()
	defer st.mu.RUnlock()

	key := getProcessKey(name, instanceID)
	if process, exists := st.processes[key]; exists {
		process.mu.Lock()
		process.PID = pid
		process.mu.Unlock()
		st.logger.Debug("Set PID %d for process %s:%d", pid, name, instanceID)
	}
}

// SetExitStatus sets the exit status for a process
func (st *StateTracker) SetExitStatus(name string, instanceID int, exitStatus int, expected bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()

	key := getProcessKey(name, instanceID)
	if process, exists := st.processes[key]; exists {
		process.mu.Lock()
		process.ExitStatus = exitStatus
		process.ExpectedExit = expected
		process.mu.Unlock()
		st.logger.Debug("Set exit status %d for process %s:%d (expected: %v)",
			exitStatus, name, instanceID, expected)
	}
}

// GetProcessInfo returns information about a specific process instance
func (st *StateTracker) GetProcessInfo(name string, instanceID int) (*ProcessInfo, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()

	key := getProcessKey(name, instanceID)
	process, exists := st.processes[key]
	if !exists {
		return nil, false
	}

	process.mu.RLock()
	defer process.mu.RUnlock()

	// Create a copy to avoid race conditions
	info := &ProcessInfo{
		Name:         process.Name,
		InstanceID:   process.InstanceID,
		Group:        process.Group,
		State:        process.State,
		Description:  process.Description,
		PID:          process.PID,
		StartTime:    process.StartTime,
		StopTime:     process.StopTime,
		ExitStatus:   process.ExitStatus,
		Retries:      process.Retries,
		MaxRetries:   process.MaxRetries,
		AutoRestart:  process.AutoRestart,
		ExpectedExit: process.ExpectedExit,
	}

	// Calculate uptime for running processes
	if process.State == RUNNING && !process.StartTime.IsZero() {
		info.Uptime = time.Since(process.StartTime)
	}

	return info, true
}

// GetAllProcesses returns information about all tracked processes
func (st *StateTracker) GetAllProcesses() map[string]*ProcessInfo {
	st.mu.RLock()
	defer st.mu.RUnlock()

	result := make(map[string]*ProcessInfo)
	for _, process := range st.processes {
		process.mu.RLock()

		// Create a copy
		info := &ProcessInfo{
			Name:         process.Name,
			InstanceID:   process.InstanceID,
			Group:        process.Group,
			State:        process.State,
			Description:  process.Description,
			PID:          process.PID,
			StartTime:    process.StartTime,
			StopTime:     process.StopTime,
			ExitStatus:   process.ExitStatus,
			Retries:      process.Retries,
			MaxRetries:   process.MaxRetries,
			AutoRestart:  process.AutoRestart,
			ExpectedExit: process.ExpectedExit,
		}

		// Calculate uptime for running processes
		if process.State == RUNNING && !process.StartTime.IsZero() {
			info.Uptime = time.Since(process.StartTime)
		}

		key := getProcessKey(process.Name, process.InstanceID)
		result[key] = info
		process.mu.RUnlock()
	}

	return result
}

// GetProcessesByName returns all instances of a specific program
func (st *StateTracker) GetProcessesByName(name string) []*ProcessInfo {
	st.mu.RLock()
	defer st.mu.RUnlock()

	var result []*ProcessInfo
	for _, process := range st.processes {
		if process.Name == name {
			process.mu.RLock()

			// Create a copy
			info := &ProcessInfo{
				Name:         process.Name,
				InstanceID:   process.InstanceID,
				Group:        process.Group,
				State:        process.State,
				Description:  process.Description,
				PID:          process.PID,
				StartTime:    process.StartTime,
				StopTime:     process.StopTime,
				ExitStatus:   process.ExitStatus,
				Retries:      process.Retries,
				MaxRetries:   process.MaxRetries,
				AutoRestart:  process.AutoRestart,
				ExpectedExit: process.ExpectedExit,
			}

			// Calculate uptime for running processes
			if process.State == RUNNING && !process.StartTime.IsZero() {
				info.Uptime = time.Since(process.StartTime)
			}

			result = append(result, info)
			process.mu.RUnlock()
		}
	}

	return result
}

// IsProcessRunning checks if a specific process instance is in RUNNING state
func (st *StateTracker) IsProcessRunning(name string, instanceID int) bool {
	info, exists := st.GetProcessInfo(name, instanceID)
	return exists && info.State == RUNNING
}

// GetRunningCount returns the number of running instances for a program
func (st *StateTracker) GetRunningCount(name string) int {
	processes := st.GetProcessesByName(name)
	count := 0
	for _, process := range processes {
		if process.State == RUNNING {
			count++
		}
	}
	return count
}

// RemoveProcess removes a process from state tracking (for cleanup)
func (st *StateTracker) RemoveProcess(name string, instanceID int) {
	st.mu.Lock()
	defer st.mu.Unlock()

	key := getProcessKey(name, instanceID)
	if _, exists := st.processes[key]; exists {
		delete(st.processes, key)
		st.logger.Info("Removed process %s:%d from state tracking", name, instanceID)
	}
}
