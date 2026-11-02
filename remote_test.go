package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func localTestStore(name string, now time.Time) *Store {
	s := newStore()
	s.name = name
	r := heartbeat(now)
	s.apply("filesystem", []Observation{{ID: "same-local-id", Source: "filesystem", Report: &r}}, nil, now)
	s.lastPoll = now
	return s
}

func testRemotes(t *testing.T, configs ...RemoteConfig) []remoteMonitor {
	t.Helper()
	remotes, err := remoteMonitors(configs)
	if err != nil {
		t.Fatal(err)
	}
	return remotes
}

func TestReciprocalFederationDoesNotLoopOrDuplicate(t *testing.T) {
	now := time.Now().UTC()
	a, b, c := localTestStore("a", now), localTestStore("b", now), localTestStore("c", now)
	serverA := httptest.NewServer(dashboard(a))
	defer serverA.Close()
	serverB := httptest.NewServer(dashboard(b))
	defer serverB.Close()
	serverC := httptest.NewServer(dashboard(c))
	defer serverC.Close()
	remoteA := testRemotes(t, RemoteConfig{Name: "b", URL: serverB.URL}, RemoteConfig{Name: "c", URL: serverC.URL})
	remoteB := testRemotes(t, RemoteConfig{Name: "a", URL: serverA.URL}, RemoteConfig{Name: "c", URL: serverC.URL})
	remoteC := testRemotes(t, RemoteConfig{Name: "a", URL: serverA.URL}, RemoteConfig{Name: "b", URL: serverB.URL})
	a.configureRemotes(remoteA)
	b.configureRemotes(remoteB)
	c.configureRemotes(remoteC)
	for i := 0; i < 4; i++ {
		a.collectRemotes(context.Background(), remoteA)
		b.collectRemotes(context.Background(), remoteB)
		c.collectRemotes(context.Background(), remoteC)
	}
	for _, s := range []*Store{a, b, c} {
		snapshot := s.snapshot(now)
		if len(snapshot.Observations) != 3 || len(s.localSnapshot(now).Observations) != 1 {
			t.Fatalf("unexpected federation: %+v", snapshot)
		}
		for _, entry := range snapshot.Observations {
			if entry.Status != "healthy" || !entry.Report.UpdatedAt.Equal(now) {
				t.Fatalf("heartbeat changed: %+v", entry)
			}
		}
		out := httptest.NewRecorder()
		dashboard(s).ServeHTTP(out, httptest.NewRequest("GET", "/", nil))
		if !strings.Contains(out.Body.String(), "<h2>a</h2>") || strings.Count(out.Body.String(), "class=\"service\"") != 3 {
			t.Fatal(out.Body.String())
		}
	}
}

func TestRemoteAuthErrorsPersistenceAndExpiry(t *testing.T) {
	now := time.Now().UTC()
	peer := localTestStore("peer", now)
	mode := "ok" // Requests complete before the next mode is assigned.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "reader" || password != "private-password" {
			w.WriteHeader(401)
			return
		}
		if mode == "fail" {
			w.WriteHeader(503)
			w.Write([]byte("private-password"))
			return
		}
		if mode == "bad" {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"version":1,"monitor":"peer","observations":[{}]}`))
			return
		}
		dashboard(peer).ServeHTTP(w, r)
	}))
	defer server.Close()
	t.Setenv("GOHC_TEST_REMOTE_PASSWORD", "private-password")
	remotes := testRemotes(t, RemoteConfig{Name: "peer", URL: server.URL + "/api/status", Username: "reader", PasswordEnv: "GOHC_TEST_REMOTE_PASSWORD"})
	s := localTestStore("local", now)
	s.configureRemotes(remotes)
	s.collectRemotes(context.Background(), remotes)
	if len(s.snapshot(now).Observations) != 2 {
		t.Fatal(s.snapshot(now))
	}
	for _, modeValue := range []string{"fail", "bad"} {
		mode = modeValue
		s.collectRemotes(context.Background(), remotes)
		snapshot := s.snapshot(now)
		for _, entry := range snapshot.Observations {
			if entry.Origin != "" && (entry.Status != "source_unavailable" || !entry.Report.UpdatedAt.Equal(now)) {
				t.Fatal(entry)
			}
		}
		if s.selfHealth(now, time.Minute).Status != "healthy" {
			t.Fatal("remote outage changed local self health")
		}
		data, _ := json.Marshal(snapshot)
		if strings.Contains(string(data), "private-password") || strings.Contains(string(data), server.URL) {
			t.Fatal("credential or URL leaked")
		}
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := s.save(path); err != nil {
		t.Fatal(err)
	}
	restored := newStore()
	if err := restored.load(path); err != nil {
		t.Fatal(err)
	}
	restored.configureRemotes(remotes)
	if len(restored.snapshot(now).Observations) != 2 {
		t.Fatal("cached remote reports lost")
	}
	mode = "ok"
	s.collectRemotes(context.Background(), remotes)
	for _, entry := range s.snapshot(now.Add(181 * time.Second)).Observations {
		if entry.Status != "stale" {
			t.Fatal("poll renewed report TTL", entry)
		}
	}
	restored.configureRemotes(nil)
	if len(restored.snapshot(now).Observations) != 1 {
		t.Fatal("removed remote retained")
	}
}

func TestRemoteRetainsPeerLocalSourceFailuresAndDockerBadge(t *testing.T) {
	now := time.Now().UTC()
	peer := newStore()
	peer.name = "peer"
	r := heartbeat(now)
	peer.apply("docker", []Observation{{ID: "docker:one", Source: "docker", ContainerID: "one", ContainerState: "running", Report: &r}}, nil, now)
	peer.machine = &MachineStatus{CPUCores: 4, Memory: &ResourceUsage{Percent: 42}}
	server := httptest.NewServer(dashboard(peer))
	defer server.Close()
	remotes := testRemotes(t, RemoteConfig{Name: "peer", URL: server.URL})
	s := localTestStore("local", now)
	s.configureRemotes(remotes)
	s.collectRemotes(context.Background(), remotes)
	view := dashboardData(s.snapshot(now))
	if len(view.Groups) != 2 || !view.Groups[1].Rows[0].Containerized || len(view.Groups[1].Machine) != 3 {
		t.Fatalf("%+v", view)
	}
	peer.apply("docker", nil, os.ErrPermission, now)
	s.collectRemotes(context.Background(), remotes)
	for _, entry := range s.snapshot(now).Observations {
		if entry.Origin == "peer" && entry.Status != "source_unavailable" {
			t.Fatal(entry)
		}
	}
}

func TestRemoteRejectsRedirectsAndFederatedResponses(t *testing.T) {
	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	remote := testRemotes(t, RemoteConfig{Name: "peer", URL: redirect.URL})[0]
	if _, err := remote.scan(context.Background(), time.Now()); err == nil || redirected {
		t.Fatal("redirect followed")
	}
	now := time.Now().UTC()
	peer := localTestStore("peer", now)
	r := heartbeat(now)
	peer.apply("remote:another", []Observation{{ID: "remote:another:one", Source: "remote:another", Origin: "another", Report: &r}}, nil, now)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(peer.snapshot(now))
	}))
	defer server.Close()
	remote = testRemotes(t, RemoteConfig{Name: "peer", URL: server.URL})[0]
	if _, err := remote.scan(context.Background(), now); err == nil {
		t.Fatal("accepted recursively imported data")
	}
}

func TestRemoteConfigurationValidation(t *testing.T) {
	for _, configs := range [][]RemoteConfig{
		{{Name: "../bad", URL: "https://example.com"}},
		{{Name: "one", URL: "https://user:secret@example.com"}},
		{{Name: "one", URL: "https://example.com?token=secret"}},
		{{Name: "one", URL: "https://example.com/other"}},
		{{Name: "one", URL: "file:///tmp/test"}},
		{{Name: "one", URL: "https://example.com", Username: "user"}},
		{{Name: "one", URL: "https://example.com", Username: "user", PasswordEnv: "GOHC_MISSING_TEST_PASSWORD"}},
		{{Name: "same", URL: "https://one.com"}, {Name: "same", URL: "https://two.com"}},
		{{Name: "one", URL: "https://one.com"}, {Name: "two", URL: "https://one.com/api/local"}},
	} {
		if _, err := remoteMonitors(configs); err == nil {
			t.Fatalf("accepted %+v", configs)
		}
	}
	if _, err := remoteMonitors([]RemoteConfig{{Name: "offline", URL: "https://example.com", Username: "reader", PasswordEnv: "GOHC_MISSING_TEST_PASSWORD"}}, false); err != nil {
		t.Fatal("cached status should not require remote credentials", err)
	}
}

func TestSlowPeerDoesNotBlockHealthyPeerAndHonorsCancellation(t *testing.T) {
	now := time.Now().UTC()
	peer := localTestStore("fast", now)
	fast := httptest.NewServer(dashboard(peer))
	defer fast.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	remotes := testRemotes(t, RemoteConfig{Name: "fast", URL: fast.URL}, RemoteConfig{Name: "slow", URL: slow.URL})
	s := localTestStore("local", now)
	s.configureRemotes(remotes)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	s.collectRemotes(ctx, remotes)
	if time.Since(started) > 2*time.Second || len(s.snapshot(now).Observations) != 2 || s.snapshot(now).Sources["remote:slow"].Error == "" {
		t.Fatal(s.snapshot(now))
	}
}

func TestRemoteBoundsAndStatusPreservation(t *testing.T) {
	now := time.Now().UTC()
	peer := localTestStore("peer", now)
	mode := "ok"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if mode == "oversized" {
			w.Write([]byte(strings.Repeat(" ", 4*1024*1024+1)))
			return
		}
		snapshot := peer.localSnapshot(now)
		if mode == "old" {
			snapshot.Version = 0
		}
		if mode == "large-report" {
			snapshot.Observations[0].Report.Message = strings.Repeat("x", 65536)
		}
		json.NewEncoder(w).Encode(snapshot)
	}))
	defer server.Close()
	remote := testRemotes(t, RemoteConfig{Name: "peer", URL: server.URL})
	for _, bad := range []string{"old", "oversized", "large-report"} {
		mode = bad
		if _, err := remote[0].scan(context.Background(), now); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	// Restore the report mutated by the intentionally invalid fixture.
	r := heartbeat(now)
	peer.apply("filesystem", []Observation{{ID: "same-local-id", Source: "filesystem", Report: &r}}, nil, now)
	mode = "ok"
	for _, status := range []string{"paused", "exited", "dead", "stopped", "unhealthy", "stale", "missing", "invalid", "awaiting_verification"} {
		peer.mu.Lock()
		entry := peer.entries["same-local-id"]
		entry.ContainerState = ""
		entry.Error = ""
		entry.Present = true
		entry.Verified = true
		reportCopy := heartbeat(now)
		switch status {
		case "paused", "exited", "dead":
			entry.ContainerState = status
		case "stale":
			reportCopy.UpdatedAt = now.Add(-181 * time.Second)
		case "missing":
			entry.Present = false
		case "invalid":
			entry.Error = "unreadable"
		case "awaiting_verification":
			entry.Verified = false
		default:
			reportCopy.Status = status
		}
		entry.Report = &reportCopy
		peer.entries[entry.ID] = entry
		peer.mu.Unlock()
		s := newStore()
		s.collectRemotes(context.Background(), remote)
		if got := s.snapshot(now).Observations[0].Status; got != status {
			t.Fatalf("%s became %s", status, got)
		}
	}
}
