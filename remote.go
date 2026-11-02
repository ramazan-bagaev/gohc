package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// RemoteConfig points at a monitor base URL or its /api/local endpoint.
// Credentials are deliberately never stored in snapshots or exported by the API.
type RemoteConfig struct {
	Name        string `json:"name"`
	URL         string `json:"url"`
	Username    string `json:"username,omitempty"`
	PasswordEnv string `json:"password_env,omitempty"`
	Timeout     string `json:"timeout,omitempty"`
}

type remoteMonitor struct {
	name, endpoint, username, password string
	client                             *http.Client
}

var remoteName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func remoteMonitors(configs []RemoteConfig, requireCredentials ...bool) ([]remoteMonitor, error) {
	if len(configs) > 32 {
		return nil, errors.New("at most 32 remote monitors are supported")
	}
	result := make([]remoteMonitor, 0, len(configs))
	names, endpoints := map[string]bool{}, map[string]bool{}
	for _, config := range configs {
		if !remoteName.MatchString(config.Name) || names[config.Name] {
			return nil, errors.New("remote names must be unique and contain 1–64 letters, digits, dots, underscores or hyphens")
		}
		names[config.Name] = true
		u, err := url.Parse(config.URL)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("remote %s: use an HTTP(S) URL without credentials, query or fragment", config.Name)
		}
		// /api/status is accepted as input, but sharing always uses local-only data.
		switch strings.TrimRight(u.Path, "/") {
		case "", "/api/status", "/api/local":
			u.Path, u.RawPath = "/api/local", ""
		default:
			return nil, fmt.Errorf("remote %s: use the monitor base URL or /api/local", config.Name)
		}
		endpoint := u.String()
		if endpoints[endpoint] {
			return nil, errors.New("remote monitor URLs must be unique")
		}
		endpoints[endpoint] = true
		if (config.Username == "") != (config.PasswordEnv == "") {
			return nil, fmt.Errorf("remote %s: username and password_env must be supplied together", config.Name)
		}
		password := ""
		if config.PasswordEnv != "" {
			password = os.Getenv(config.PasswordEnv)
			if password == "" && (len(requireCredentials) == 0 || requireCredentials[0]) {
				return nil, fmt.Errorf("remote %s: password environment variable is empty", config.Name)
			}
		}
		timeout := 10 * time.Second
		if config.Timeout != "" {
			var err error
			timeout, err = time.ParseDuration(config.Timeout)
			if err != nil || timeout < time.Second || timeout > 30*time.Second {
				return nil, fmt.Errorf("remote %s: timeout must be between one and 30 seconds", config.Name)
			}
		}
		result = append(result, remoteMonitor{
			name: config.Name, endpoint: endpoint, username: config.Username, password: password,
			client: &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		})
	}
	return result, nil
}

func (remote remoteMonitor) scan(ctx context.Context, now time.Time) (Snapshot, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, remote.endpoint, nil)
	if err != nil {
		return Snapshot{}, errors.New("invalid remote request")
	}
	request.Header.Set("Accept", "application/json")
	if remote.username != "" {
		request.SetBasicAuth(remote.username, remote.password)
	}
	response, err := remote.client.Do(request)
	if err != nil {
		// Do not echo URLs, redirects, response bodies, or authentication data.
		return Snapshot{}, errors.New("remote request failed or timed out")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Snapshot{}, fmt.Errorf("remote returned HTTP %d", response.StatusCode)
	}
	contentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		return Snapshot{}, errors.New("remote did not return JSON")
	}
	const maxSnapshot = 4 * 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(response.Body, maxSnapshot+1))
	if err != nil || len(data) > maxSnapshot {
		return Snapshot{}, errors.New("remote snapshot unreadable or larger than 4 MiB")
	}
	var snapshot Snapshot
	if err = json.Unmarshal(data, &snapshot); err != nil || snapshot.Version != 1 || snapshot.Monitor == "" || len(snapshot.Monitor) > 256 || snapshot.Observations == nil || len(snapshot.Observations) > 10000 {
		return Snapshot{}, errors.New("invalid local snapshot; remote requires gohc v0.2.0 or newer")
	}
	seen := map[string]bool{}
	for _, entry := range snapshot.Observations {
		if entry.ID == "" || len(entry.ID) > 4096 || seen[entry.ID] || entry.Origin != "" || strings.HasPrefix(entry.Source, "remote:") || entry.Report == nil {
			return Snapshot{}, errors.New("invalid or federated entry in local snapshot")
		}
		seen[entry.ID] = true
		encodedReport, err := json.Marshal(entry.Report)
		if err != nil || len(encodedReport)+1 > 64*1024 {
			return Snapshot{}, errors.New("remote report exceeds 64 KiB")
		}
		if err := entry.Report.Validate(now); err != nil || len(entry.Error) > 4096 {
			return Snapshot{}, errors.New("invalid report in remote snapshot")
		}
		switch entry.Status {
		case "healthy", "unhealthy", "unknown", "stopped", "stale", "missing", "invalid", "awaiting_verification", "source_unavailable", "running", "created", "paused", "restarting", "exited", "dead", "removing":
		default:
			return Snapshot{}, errors.New("unsupported remote status")
		}
	}
	return snapshot, nil
}

func (s *Store) configureRemotes(remotes []remoteMonitor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	active := map[string]bool{}
	for _, remote := range remotes {
		key := "remote:" + remote.name
		active[key] = true
		s.sources[key] = SourceState{Error: "Not yet collected"}
	}
	for source := range s.sources {
		if strings.HasPrefix(source, "remote:") && !active[source] {
			delete(s.sources, source)
		}
	}
	for id, entry := range s.entries {
		if entry.Origin != "" && !active[entry.Source] {
			delete(s.entries, id)
		}
	}
}

func (s *Store) collectRemotes(ctx context.Context, remotes []remoteMonitor) {
	var done sync.WaitGroup
	for _, remote := range remotes {
		done.Add(1)
		go func(remote remoteMonitor) {
			defer done.Done()
			snapshot, err := remote.scan(ctx, time.Now().UTC())
			source := "remote:" + remote.name
			entries := make([]Observation, 0, len(snapshot.Observations))
			if err == nil {
				for _, entry := range snapshot.Observations {
					entry.ID = source + ":" + entry.ID
					entry.Origin, entry.OriginSource = remote.name, entry.Source
					entry.Source, entry.RemoteStatus = source, entry.Status
					entries = append(entries, entry)
				}
			}
			s.apply(source, entries, err, time.Now().UTC())
			if err == nil {
				s.mu.Lock()
				state := s.sources[source]
				state.Machine = snapshot.Machine
				s.sources[source] = state
				s.mu.Unlock()
			}
		}(remote)
	}
	done.Wait()
}

func (s *Store) localSnapshot(now time.Time) Snapshot {
	snapshot := s.snapshot(now)
	local := make([]Observation, 0, len(snapshot.Observations))
	for _, entry := range snapshot.Observations {
		if entry.Origin == "" && !strings.HasPrefix(entry.Source, "remote:") {
			local = append(local, entry)
		}
	}
	snapshot.Observations = local
	for source := range snapshot.Sources {
		if strings.HasPrefix(source, "remote:") {
			delete(snapshot.Sources, source)
		}
	}
	return snapshot
}
