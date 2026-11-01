package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

const defaultUpdateInterval = time.Minute

type TimingConfig struct {
	PollInterval     string `json:"poll_interval,omitempty"`
	MachineInterval  string `json:"machine_interval,omitempty"`
	DashboardRefresh string `json:"dashboard_refresh,omitempty"`
}

func loadTimingConfig(path string, required bool, explicit map[string]bool, poll, machine, refresh *time.Duration) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) && !required {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read timing config: %w", err)
	}
	if len(data) > 64*1024 {
		return fmt.Errorf("timing config exceeds 64 KiB")
	}
	var config TimingConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&config); err != nil {
		return fmt.Errorf("timing config: %w", err)
	}
	if err = decoder.Decode(new(interface{})); err != io.EOF {
		return fmt.Errorf("timing config must contain one JSON object")
	}
	for _, field := range []struct {
		name, value string
		target      *time.Duration
	}{
		{"interval", config.PollInterval, poll}, {"machine-interval", config.MachineInterval, machine}, {"refresh", config.DashboardRefresh, refresh},
	} {
		if field.value == "" {
			continue
		}
		duration, e := time.ParseDuration(field.value)
		if e != nil {
			return fmt.Errorf("timing config %s: %w", field.name, e)
		}
		if duration < time.Second || duration > 24*time.Hour {
			return fmt.Errorf("%s must be between one second and 24 hours", field.name)
		}
		if !explicit[field.name] {
			*field.target = duration
		}
	}
	return nil
}
