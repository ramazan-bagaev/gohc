package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCPUPercentageIgnoresGuestDoubleCounting(t *testing.T) {
	first, e := parseCPU("cpu 100 0 100 800 0 0 0 0 90 0\ncpu0 0")
	if e != nil {
		t.Fatal(e)
	}
	second, e := parseCPU("cpu 130 0 120 850 0 0 0 0 110 0\n")
	if e != nil {
		t.Fatal(e)
	}
	if got := cpuUsage(first, second); got == nil || *got != 50 {
		t.Fatal(got)
	}
	if cpuUsage(cpuCounters{}, second) != nil || cpuUsage(second, first) != nil {
		t.Fatal("first/reset sample must be unavailable")
	}
	for _, text := range []string{"", "cpu 1 2", "cpu 1 nope 2 3", "cpu 18446744073709551615 2 3 4"} {
		if _, e := parseCPU(text); e == nil {
			t.Fatal("accepted invalid CPU counters", text)
		}
	}
}
func TestMemoryUsesAvailableInsteadOfFree(t *testing.T) {
	value, e := parseMemory("MemTotal: 1000 kB\nMemFree: 100 kB\nMemAvailable: 600 kB\nCached: 800 kB\n")
	if e != nil || value.UsedBytes != 400*1024 || value.Percent != 40 {
		t.Fatal(value, e)
	}
	for _, text := range []string{"", "MemTotal: 10 kB", "MemTotal: 10 kB\nMemAvailable: 20 kB", "MemTotal: 0 kB\nMemAvailable: 0 kB"} {
		if _, e := parseMemory(text); e == nil {
			t.Fatal("accepted invalid memory", text)
		}
	}
}
func TestLinuxMachineReadingsAndUnavailableFiles(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "stat"), []byte("cpu 100 0 100 800"), 0600)
	os.WriteFile(filepath.Join(root, "meminfo"), []byte("MemTotal: 1000 kB\nMemAvailable: 600 kB"), 0600)
	os.WriteFile(filepath.Join(root, "uptime"), []byte("123.5 1000.0"), 0600)
	status := MachineStatus{Errors: map[string]string{}}
	counters := readLinuxMachine(root, cpuCounters{}, &status)
	if status.CPUPercent != nil || status.Memory == nil || status.UptimeSeconds == nil || *status.UptimeSeconds != 123.5 || len(status.Errors) != 0 {
		t.Fatalf("%+v", status)
	}
	os.WriteFile(filepath.Join(root, "stat"), []byte("cpu 130 0 120 850"), 0600)
	status = MachineStatus{Errors: map[string]string{}}
	readLinuxMachine(root, counters, &status)
	if status.CPUPercent == nil || math.Abs(*status.CPUPercent-50) > .001 {
		t.Fatalf("%+v", status)
	}
	status = MachineStatus{Errors: map[string]string{}}
	readLinuxMachine(filepath.Join(root, "absent"), counters, &status)
	if len(status.Errors) != 3 || status.Memory != nil || status.CPUPercent != nil {
		t.Fatalf("%+v", status)
	}
}
func TestMachineSummaryKeepsDetailsInAPI(t *testing.T) {
	s := newStore()
	cpu := 23.0
	s.machine = &MachineStatus{Hostname: "internal-machine", SampledAt: time.Now().UTC(), CPUPercent: &cpu, Memory: &ResourceUsage{Percent: 40, TotalBytes: 1024}, Disk: &ResourceUsage{Percent: 55, Path: "/private/path"}}
	w := httptest.NewRecorder()
	dashboard(s).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	html := w.Body.String()
	for _, want := range []string{"CPU", "Memory", "Disk", "23%", "40%", "55%"} {
		if !strings.Contains(html, want) {
			t.Fatal("missing", want)
		}
	}
	for _, hidden := range []string{"internal-machine", "/private/path", "total_bytes"} {
		if strings.Contains(html, hidden) {
			t.Fatal("exposed detail", hidden)
		}
	}
	api := httptest.NewRecorder()
	dashboard(s).ServeHTTP(api, httptest.NewRequest("GET", "/api/status", nil))
	var decoded Snapshot
	if e := json.Unmarshal(api.Body.Bytes(), &decoded); e != nil {
		t.Fatal(e)
	}
	if decoded.Machine == nil || decoded.Machine.Hostname != "internal-machine" || decoded.Machine.Disk.Path != "/private/path" {
		t.Fatal(api.Body.String())
	}
}
func TestMachineSamplerAndDisk(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("disk backend unavailable")
	}
	root := t.TempDir()
	s := newStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	machineLoop(ctx, s, root)
	m := s.snapshot(time.Now()).Machine
	if m == nil || m.Disk == nil || m.Disk.TotalBytes == 0 || m.Disk.UsedBytes > m.Disk.TotalBytes || m.Disk.Percent < 0 || m.Disk.Percent > 100 || m.SampledAt.IsZero() {
		t.Fatalf("%+v", m)
	}
}
