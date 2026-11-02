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

type MonitorConfig struct {
	Name             string         `json:"name,omitempty"`
	Remotes          []RemoteConfig `json:"remotes,omitempty"`
	PollInterval     string         `json:"poll_interval,omitempty"`
	MachineInterval  string         `json:"machine_interval,omitempty"`
	DashboardRefresh string         `json:"dashboard_refresh,omitempty"`
}

func loadMonitorConfig(path string, required bool, explicit map[string]bool, poll, machine, refresh *time.Duration, loaded ...*MonitorConfig) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) && !required {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read monitor config: %w", err)
	}
	if len(data) > 64*1024 {
		return fmt.Errorf("monitor config exceeds 64 KiB")
	}
	var config MonitorConfig
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&config); err != nil {
		return fmt.Errorf("monitor config: %w", err)
	}
	if err = decoder.Decode(new(interface{})); err != io.EOF {
		return fmt.Errorf("monitor config must contain one JSON object")
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
			return fmt.Errorf("monitor config %s: %w", field.name, e)
		}
		if duration < time.Second || duration > 24*time.Hour {
			return fmt.Errorf("%s must be between one second and 24 hours", field.name)
		}
		if !explicit[field.name] {
			*field.target = duration
		}
	}
	if len(loaded) > 0 {
		*loaded[0] = config
	}
	return nil
}
