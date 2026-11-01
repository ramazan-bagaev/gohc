package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type ResourceUsage struct {
	TotalBytes     uint64  `json:"total_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	Percent        float64 `json:"percent"`
	Path           string  `json:"path,omitempty"`
}
type MachineStatus struct {
	Hostname      string            `json:"hostname"`
	Platform      string            `json:"platform"`
	CPUCores      int               `json:"cpu_cores"`
	CPUPercent    *float64          `json:"cpu_percent"`
	Memory        *ResourceUsage    `json:"memory"`
	Disk          *ResourceUsage    `json:"disk"`
	UptimeSeconds *float64          `json:"uptime_seconds"`
	SampledAt     time.Time         `json:"sampled_at"`
	Errors        map[string]string `json:"errors,omitempty"`
}
type cpuCounters struct{ Total, Idle uint64 }

func parseCPU(data string) (cpuCounters, error) {
	line := strings.SplitN(data, "\n", 2)[0]
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuCounters{}, errors.New("missing aggregate CPU counters")
	}
	var c cpuCounters
	// Guest times are already included in user/nice. Sum only the first 8 fields.
	for i := 1; i < len(fields) && i <= 8; i++ {
		value, err := strconv.ParseUint(fields[i], 10, 64)
		if err != nil {
			return c, err
		}
		if ^uint64(0)-c.Total < value {
			return c, errors.New("CPU counter overflow")
		}
		c.Total += value
		if i == 4 || i == 5 {
			c.Idle += value
		}
	}
	return c, nil
}
func cpuUsage(previous, current cpuCounters) *float64 {
	if previous.Total == 0 || current.Total <= previous.Total || current.Idle < previous.Idle {
		return nil
	}
	total := current.Total - previous.Total
	idle := current.Idle - previous.Idle
	if idle > total {
		return nil
	}
	value := 100 * float64(total-idle) / float64(total)
	return &value
}
func parseMemory(data string) (*ResourceUsage, error) {
	values := map[string]uint64{}
	for _, line := range strings.Split(data, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		if key != "MemTotal" && key != "MemAvailable" {
			continue
		}
		if len(fields) < 3 || fields[2] != "kB" {
			return nil, errors.New("invalid memory units")
		}
		n, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return nil, err
		}
		if n > (^uint64(0))/1024 {
			return nil, errors.New("memory value overflow")
		}
		values[key] = n * 1024
	}
	total, ok := values["MemTotal"]
	available, hasAvailable := values["MemAvailable"]
	if !ok || !hasAvailable || total == 0 || available > total {
		return nil, errors.New("invalid or unavailable memory counters")
	}
	used := total - available
	return &ResourceUsage{TotalBytes: total, UsedBytes: used, AvailableBytes: available, Percent: 100 * float64(used) / float64(total)}, nil
}
func readLinuxMachine(proc string, previous cpuCounters, status *MachineStatus) cpuCounters {
	next := cpuCounters{}
	if b, e := os.ReadFile(filepath.Join(proc, "stat")); e != nil {
		status.Errors["cpu"] = e.Error()
	} else if next, e = parseCPU(string(b)); e != nil {
		status.Errors["cpu"] = e.Error()
	} else {
		status.CPUPercent = cpuUsage(previous, next)
	}
	if b, e := os.ReadFile(filepath.Join(proc, "meminfo")); e != nil {
		status.Errors["memory"] = e.Error()
	} else if status.Memory, e = parseMemory(string(b)); e != nil {
		status.Errors["memory"] = e.Error()
	}
	if b, e := os.ReadFile(filepath.Join(proc, "uptime")); e != nil {
		status.Errors["uptime"] = e.Error()
	} else {
		fields := strings.Fields(string(b))
		if len(fields) > 0 {
			value, e := strconv.ParseFloat(fields[0], 64)
			if e == nil && value >= 0 {
				status.UptimeSeconds = &value
			} else {
				status.Errors["uptime"] = "invalid uptime"
			}
		}
	}
	return next
}
func sampleMachine(s *Store, dir string, previous cpuCounters) cpuCounters {
	hostname, _ := os.Hostname()
	value := MachineStatus{Hostname: hostname, Platform: runtime.GOOS, CPUCores: runtime.NumCPU(), SampledAt: time.Now().UTC(), Errors: map[string]string{}}
	next := readPlatformMachine(previous, &value)
	if disk, e := readDiskUsage(dir); e != nil {
		value.Errors["disk"] = e.Error()
	} else {
		value.Disk = disk
	}
	s.mu.Lock()
	s.machine = &value
	s.mu.Unlock()
	return next
}
func machineLoop(ctx context.Context, s *Store, dir string, interval ...time.Duration) {
	duration := defaultUpdateInterval
	if len(interval) > 0 {
		duration = interval[0]
	}
	previous := sampleMachine(s, dir, cpuCounters{})
	ticker := time.NewTicker(duration)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			previous = sampleMachine(s, dir, previous)
		}
	}
}
func metricPercent(value *float64) string {
	if value == nil {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", *value)
}
