package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ramazan-bagaev/gohc/client"
	"github.com/ramazan-bagaev/gohc/report"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Observation struct {
	Origin         string         `json:"origin,omitempty"`
	OriginSource   string         `json:"origin_source,omitempty"`
	RemoteStatus   string         `json:"remote_status,omitempty"`
	ID             string         `json:"id"`
	Source         string         `json:"source"`
	Location       string         `json:"location"`
	Report         *report.Report `json:"report,omitempty"`
	ContainerID    string         `json:"container_id,omitempty"`
	ContainerName  string         `json:"container_name,omitempty"`
	ContainerState string         `json:"container_state,omitempty"`
	DockerHealth   string         `json:"docker_health,omitempty"`
	LastObserved   time.Time      `json:"last_observed"`
	Error          string         `json:"error,omitempty"`
	Verified       bool           `json:"verified"`
	Present        bool           `json:"present"`
	Status         string         `json:"status"`
}
type SourceState struct {
	Machine     *MachineStatus `json:"machine,omitempty"`
	Error       string         `json:"error,omitempty"`
	LastSuccess time.Time      `json:"last_success,omitempty"`
}
type Snapshot struct {
	Version          int                    `json:"version"`
	Monitor          string                 `json:"monitor"`
	Observations     []Observation          `json:"observations"`
	Sources          map[string]SourceState `json:"sources"`
	GeneratedAt      time.Time              `json:"generated_at"`
	PersistenceError string                 `json:"persistence_error,omitempty"`
	Machine          *MachineStatus         `json:"machine,omitempty"`
}
type Store struct {
	name             string
	mu               sync.RWMutex
	entries          map[string]Observation
	sources          map[string]SourceState
	persistenceError string
	lastPoll         time.Time
	machine          *MachineStatus
}

func newStore() *Store {
	return &Store{entries: map[string]Observation{}, sources: map[string]SourceState{}}
}
func (s *Store) apply(source string, entries []Observation, scanErr error, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.sources[source]
	if scanErr != nil {
		previous.Error = scanErr.Error()
	} else {
		previous = SourceState{LastSuccess: now}
	}
	s.sources[source] = previous
	// A failed inventory cannot establish that previous entries disappeared.
	if scanErr == nil {
		containers := map[string]Observation{}
		for _, v := range entries {
			if v.ContainerID != "" && v.Report == nil && strings.Count(v.ID, ":") == 2 {
				containers[v.ContainerID] = v
			}
		}
		for k, v := range s.entries {
			if v.Source == source {
				v.Present = false
				v.Verified = true
				if c, ok := containers[v.ContainerID]; ok {
					v.ContainerState = c.ContainerState
					v.DockerHealth = c.DockerHealth
					v.ContainerName = c.ContainerName
					v.Error = c.Error
				}
				s.entries[k] = v
			}
		}
	}
	for _, v := range entries {
		old := s.entries[v.ID]
		// Discovery metadata and invalid first reports do not register services.
		if v.Report == nil && old.Report == nil {
			continue
		}
		// Preserve the last valid heartbeat when a report becomes unreadable.
		if v.Report == nil && v.Error != "" {
			v.Report = old.Report
		}
		v.Verified = true
		v.Present = true
		v.LastObserved = now
		s.entries[v.ID] = v
	}
}
func (s *Store) snapshot(now time.Time) Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := Snapshot{Version: 1, Monitor: s.name, Sources: map[string]SourceState{}, GeneratedAt: now, Observations: []Observation{}, PersistenceError: s.persistenceError, Machine: s.machine}
	for k, v := range s.sources {
		result.Sources[k] = v
	}
	for _, v := range s.entries {
		if v.Report == nil {
			continue
		}
		switch {
		case !v.Verified:
			v.Status = "awaiting_verification"
		case result.Sources[v.Source].Error != "":
			v.Status = "source_unavailable"
		case v.Error != "":
			v.Status = "invalid"
		case v.Origin != "" && v.RemoteStatus != "" && v.RemoteStatus != "healthy" && v.RemoteStatus != "unhealthy" && v.RemoteStatus != "unknown":
			v.Status = v.RemoteStatus
		case v.ContainerState != "" && v.ContainerState != "running":
			v.Status = v.ContainerState
		case v.Report.Status == "stopped":
			v.Status = "stopped"
		case !now.Before(v.Report.UpdatedAt.Add(time.Duration(v.Report.TTLSeconds) * time.Second)):
			v.Status = "stale"
		case !v.Present:
			v.Status = "missing"
		default:
			v.Status = v.Report.Status
			if v.Origin != "" && v.RemoteStatus != "" {
				v.Status = v.RemoteStatus
			}
		}
		result.Observations = append(result.Observations, v)
	}
	sort.Slice(result.Observations, func(i, j int) bool { return result.Observations[i].ID < result.Observations[j].ID })
	return result
}
func (s *Store) load(path string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var data struct {
		Version      int           `json:"version"`
		Observations []Observation `json:"observations"`
	}
	if err = json.Unmarshal(b, &data); err != nil {
		return err
	}
	if data.Version != 1 {
		return errors.New("unsupported state version")
	}
	for _, v := range data.Observations {
		if v.Report == nil {
			continue
		}
		v.Verified = false
		v.Present = false
		v.Status = "awaiting_verification"
		s.entries[v.ID] = v
	}
	return nil
}
func (s *Store) save(path string) error {
	snap := s.snapshot(time.Now())
	b, err := json.MarshalIndent(struct {
		Version      int           `json:"version"`
		Observations []Observation `json:"observations"`
	}{1, snap.Observations}, "", "  ")
	if err != nil {
		return err
	}
	err = atomicState(path, b)
	s.mu.Lock()
	if err != nil {
		s.persistenceError = err.Error()
	} else {
		s.persistenceError = ""
	}
	s.mu.Unlock()
	return err
}
func atomicState(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func scanFiles(dir string, now time.Time) ([]Observation, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	entries := []Observation{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == root {
				return walkErr
			}
			entries = append(entries, Observation{ID: "filesystem:" + path, Source: "filesystem", Location: path, Error: walkErr.Error()})
			return nil
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || filepath.Ext(path) != ".json" {
			return nil
		}
		// State lives elsewhere by default, and is never interpreted as a heartbeat.
		v := Observation{ID: "filesystem:" + path, Source: "filesystem", Location: path}
		if !d.Type().IsRegular() {
			v.Error = "report is not a regular file"
			entries = append(entries, v)
			return nil
		}
		f, e := os.Open(path)
		if e == nil {
			var r report.Report
			r, e = report.Read(f, now)
			f.Close()
			if e == nil {
				v.Report = &r
			}
		}
		if e != nil {
			v.Error = e.Error()
		}
		entries = append(entries, v)
		return nil
	})
	if os.IsNotExist(err) {
		return entries, fmt.Errorf("report directory not found: %s (clients create it on first report)", root)
	}
	return entries, err
}

// Self-health follows completed collection cycles, not just a live timer.
func (s *Store) selfHealth(now time.Time, interval time.Duration) client.Health {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.lastPoll.IsZero() {
		return client.Health{Status: "unknown", Message: "Starting collection"}
	}
	if now.Sub(s.lastPoll) > 2*interval+35*time.Second {
		return client.Health{Status: "unhealthy", Message: "Collection loop has stopped making progress"}
	}
	if s.persistenceError != "" {
		return client.Health{Status: "unhealthy", Message: "Cannot persist observations"}
	}
	for name, source := range s.sources {
		if source.Error != "" && !strings.HasPrefix(name, "remote:") {
			return client.Health{Status: "unhealthy", Message: name + " collection unavailable"}
		}
	}
	return client.Health{Status: "healthy"}
}
