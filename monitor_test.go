package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/ramazan-bagaev/gohc/report"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func heartbeat(now time.Time) report.Report {
	return report.Report{Version: 1, Service: "demo", Instance: "one", Status: "healthy", UpdatedAt: now, TTLSeconds: 60}
}
func TestRereadingDoesNotRenewHeartbeat(t *testing.T) {
	now := time.Now().UTC()
	r := heartbeat(now)
	s := newStore()
	v := Observation{ID: "file", Source: "filesystem", Report: &r}
	s.apply("filesystem", []Observation{v}, nil, now)
	s.apply("filesystem", []Observation{v}, nil, now.Add(61*time.Second))
	if got := s.snapshot(now.Add(61 * time.Second)).Observations[0].Status; got != "stale" {
		t.Fatal(got)
	}
}
func TestSourceFailurePreservesObservation(t *testing.T) {
	now := time.Now().UTC()
	r := heartbeat(now)
	s := newStore()
	s.apply("docker", []Observation{{ID: "one", Source: "docker", Report: &r}}, nil, now)
	s.apply("docker", nil, errors.New("daemon unavailable"), now.Add(time.Second))
	v := s.snapshot(now).Observations[0]
	if v.Status != "source_unavailable" || v.Report == nil || !v.Present {
		t.Fatalf("%+v", v)
	}
}
func TestPersistenceRequiresReverification(t *testing.T) {
	now := time.Now().UTC()
	r := heartbeat(now)
	s := newStore()
	s.apply("filesystem", []Observation{{ID: "one", Source: "filesystem", Report: &r}}, nil, now)
	p := filepath.Join(t.TempDir(), "state", "snapshot.json")
	if e := s.save(p); e != nil {
		t.Fatal(e)
	}
	next := newStore()
	if e := next.load(p); e != nil {
		t.Fatal(e)
	}
	if got := next.snapshot(now).Observations[0].Status; got != "awaiting_verification" {
		t.Fatal(got)
	}
	next.apply("filesystem", nil, nil, now)
	if got := next.snapshot(now).Observations[0].Status; got != "missing" {
		t.Fatal(got)
	}
}
func TestFilesystemIsolationAndMalformedReports(t *testing.T) {
	now := time.Now().UTC()
	dir := t.TempDir()
	if e := report.Write(filepath.Join(dir, "nested", "valid.json"), heartbeat(now)); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{"), 0600)
	outside := filepath.Join(t.TempDir(), "outside.json")
	report.Write(outside, heartbeat(now))
	os.Symlink(outside, filepath.Join(dir, "link.json"))
	entries, e := scanFiles(dir, now)
	if e != nil {
		t.Fatal(e)
	}
	if len(entries) != 2 {
		t.Fatalf("%+v", entries)
	}
	good, bad := 0, 0
	for _, v := range entries {
		if v.Report != nil {
			good++
		}
		if v.Error != "" {
			bad++
		}
	}
	if good != 1 || bad != 1 {
		t.Fatalf("good %d bad %d", good, bad)
	}
}
func TestMissingReportRetainsLastHeartbeat(t *testing.T) {
	now := time.Now().UTC()
	r := heartbeat(now)
	s := newStore()
	v := Observation{ID: "one", Source: "filesystem", Report: &r}
	s.apply("filesystem", []Observation{v}, nil, now)
	s.apply("filesystem", nil, nil, now.Add(time.Second))
	if got := s.snapshot(now.Add(time.Second)).Observations[0].Status; got != "missing" {
		t.Fatal(got)
	}
	if got := s.snapshot(now.Add(time.Minute)).Observations[0].Status; got != "stale" {
		t.Fatal(got)
	}
}
func TestDockerReadsWithoutExecAndHonorsContainerDirectory(t *testing.T) {
	now := time.Now().UTC()
	data, _ := json.Marshal(heartbeat(now))
	archive := bytes.Buffer{}
	tw := tar.NewWriter(&archive)
	tw.WriteHeader(&tar.Header{Name: "custom/heartbeat.json", Mode: 0644, Size: int64(len(data))})
	tw.Write(data)
	tw.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("unexpected mutation", r.Method)
		}
		switch r.URL.Path {
		case "/containers/json":
			w.Write([]byte(`[{"Id":"abc","Names":["/demo"],"State":"running"},{"Id":"plain","Names":["/plain"],"State":"running"}]`))
		case "/containers/abc/json":
			w.Write([]byte(`{"Config":{"Env":["GOHC_DIR=/custom"]},"State":{"Status":"running","Health":{"Status":"unhealthy"}}}`))
		case "/containers/plain/json":
			w.Write([]byte(`{"Config":{"Env":[]},"State":{"Status":"running"}}`))
		case "/containers/abc/archive":
			if r.URL.Query().Get("path") != "/custom" {
				t.Error(r.URL)
			}
			w.Write(archive.Bytes())
		case "/containers/plain/archive":
			w.WriteHeader(404)
		default:
			t.Error("unexpected endpoint", r.URL)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	dc := &dockerClient{client: server.Client(), base: server.URL, identity: "engine"}
	entries, e := dc.scan(context.Background(), now)
	if e != nil {
		t.Fatal(e)
	}
	if len(entries) != 3 {
		t.Fatalf("%+v", entries)
	}
	for _, v := range entries {
		if v.Report != nil {
			if v.Report.Service != "demo" || v.DockerHealth != "unhealthy" || v.Location != "/custom/heartbeat.json" {
				t.Fatalf("%+v", v)
			}
		} else if v.Error != "" {
			t.Fatal(v.Error)
		}
	}
}
func TestStoppedContainerUpdatesCachedApplication(t *testing.T) {
	now := time.Now().UTC()
	r := heartbeat(now)
	s := newStore()
	s.apply("docker", []Observation{{ID: "docker:engine:abc:report", Source: "docker", ContainerID: "abc", ContainerState: "running", Report: &r}}, nil, now)
	s.apply("docker", []Observation{{ID: "docker:engine:abc", Source: "docker", ContainerID: "abc", ContainerState: "exited"}}, nil, now)
	for _, v := range s.snapshot(now).Observations {
		if v.Report != nil && v.Status != "exited" {
			t.Fatalf("%+v", v)
		}
	}
}
func TestDashboardEscapesReports(t *testing.T) {
	now := time.Now().UTC()
	r := heartbeat(now)
	r.Service = "<script>alert(1)</script>"
	r.Message = "hidden diagnostic message"
	s := newStore()
	s.apply("filesystem", []Observation{{ID: "one", Source: "filesystem", Report: &r}}, nil, now)
	w := httptest.NewRecorder()
	dashboard(s).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 || strings.Contains(w.Body.String(), r.Service) || strings.Contains(w.Body.String(), r.Message) || !strings.Contains(w.Body.String(), "&lt;script&gt;") {
		t.Fatal(w.Body.String())
	}
}
func TestDockerContextSelection(t *testing.T) {
	root := t.TempDir()
	t.Setenv("DOCKER_CONFIG", root)
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_HOST", "unix:///custom.sock")
	endpoint, _, _, e := dockerEndpoint()
	if e != nil || endpoint != "unix:///custom.sock" {
		t.Fatal(endpoint, e)
	}
	t.Setenv("DOCKER_CONTEXT", "missing")
	if _, _, _, e = dockerEndpoint(); e == nil {
		t.Fatal("explicit context must override DOCKER_HOST")
	}
}
func TestDockerFailureDoesNotBlockFilesystem(t *testing.T) {
	dir := t.TempDir()
	r := heartbeat(time.Now().UTC())
	report.Write(filepath.Join(dir, "app.json"), r)
	t.Setenv("DOCKER_HOST", "unix:///definitely-missing-gohc.sock")
	t.Setenv("DOCKER_CONTEXT", "")
	s := newStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	poll(ctx, s, dir, filepath.Join(t.TempDir(), "state.json"), true, time.Second)
	snap := s.snapshot(time.Now())
	if len(snap.Observations) != 1 || snap.Observations[0].Status != "healthy" || snap.Sources["docker"].Error == "" {
		t.Fatalf("%+v", snap)
	}
}

func TestInstrumentedContainerDoesNotAppearUnmonitored(t *testing.T) {
	now := time.Now().UTC()
	r := heartbeat(now)
	s := newStore()
	s.apply("docker", []Observation{
		{ID: "docker:engine:abc", Source: "docker", ContainerID: "abc", ContainerName: "/demo", ContainerState: "running"},
		{ID: "docker:engine:abc:reports/status.json", Source: "docker", ContainerID: "abc", ContainerName: "/demo", ContainerState: "running", Report: &r},
	}, nil, now)
	for _, v := range s.snapshot(now).Observations {
		if v.Report == nil {
			t.Fatalf("%+v", v)
		}
	}
	w := httptest.NewRecorder()
	dashboard(s).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if strings.Contains(w.Body.String(), "unmonitored") || !strings.Contains(w.Body.String(), "Healthy") {
		t.Fatal(w.Body.String())
	}
}

func TestRegistrationRequiresFirstValidReport(t *testing.T) {
	now := time.Now().UTC()
	s := newStore()
	s.apply("docker", []Observation{
		{ID: "docker:engine:plain", Source: "docker", ContainerID: "plain", ContainerState: "running"},
		{ID: "docker:engine:bad:report", Source: "docker", ContainerID: "bad", Error: "invalid JSON"},
	}, nil, now)
	if len(s.snapshot(now).Observations) != 0 {
		t.Fatal("unregistered containers became services")
	}
	r := heartbeat(now)
	v := Observation{ID: "docker:engine:app:report", Source: "docker", ContainerID: "app", Report: &r}
	s.apply("docker", []Observation{v}, nil, now)
	s.apply("docker", []Observation{{ID: v.ID, Source: "docker", ContainerID: "app", Error: "invalid replacement"}}, nil, now.Add(time.Second))
	rows := s.snapshot(now).Observations
	if len(rows) != 1 || rows[0].Report == nil || rows[0].Status != "invalid" {
		t.Fatalf("%+v", rows)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if e := s.save(path); e != nil {
		t.Fatal(e)
	}
	restored := newStore()
	if e := restored.load(path); e != nil {
		t.Fatal(e)
	}
	restored.apply("docker", nil, nil, now.Add(61*time.Second))
	rows = restored.snapshot(now.Add(61 * time.Second)).Observations
	if len(rows) != 1 || rows[0].Report == nil {
		t.Fatalf("registered service disappeared: %+v", rows)
	}
}
func TestOldUnregisteredInventoryIsNotRestored(t *testing.T) {
	now := time.Now().UTC()
	r := heartbeat(now)
	b, _ := json.Marshal(map[string]interface{}{"version": 1, "observations": []Observation{
		{ID: "docker:engine:plain", Source: "docker", ContainerID: "plain"},
		{ID: "valid", Source: "filesystem", Report: &r},
	}})
	path := filepath.Join(t.TempDir(), "state.json")
	os.WriteFile(path, b, 0600)
	s := newStore()
	if e := s.load(path); e != nil {
		t.Fatal(e)
	}
	if got := len(s.snapshot(now).Observations); got != 1 {
		t.Fatalf("restored %d services", got)
	}
}
func TestSelfHealthChecksCollectionProgress(t *testing.T) {
	now := time.Now().UTC()
	s := newStore()
	interval := 2 * time.Second
	if h := s.selfHealth(now, interval); h.Status != "unknown" {
		t.Fatal(h)
	}
	s.lastPoll = now
	if h := s.selfHealth(now, interval); h.Status != "healthy" {
		t.Fatal(h)
	}
	if h := s.selfHealth(now.Add(time.Minute), interval); h.Status != "unhealthy" {
		t.Fatal(h)
	}
	s.persistenceError = "disk full"
	if h := s.selfHealth(now, interval); h.Status != "unhealthy" {
		t.Fatal(h)
	}
	s.persistenceError = ""
	s.sources["docker"] = SourceState{Error: "unavailable"}
	if h := s.selfHealth(now, interval); h.Status != "unhealthy" {
		t.Fatal(h)
	}
}

func TestDashboardUpdateTimeComesFromHeartbeatNotReadTime(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	r := heartbeat(now.Add(-30 * time.Second))
	r.Status = "unhealthy"
	s := newStore()
	s.apply("filesystem", []Observation{{ID: "one", Source: "filesystem", Report: &r}}, nil, now)
	view := dashboardData(s.snapshot(now))
	if len(view.Rows) != 1 || view.Rows[0].UpdatedAt != r.UpdatedAt.Format(time.RFC3339Nano) {
		t.Fatalf("%+v", view.Rows)
	}
	if view.Rows[0].UpdatedAt == now.Format(time.RFC3339Nano) {
		t.Fatal("read time was shown as report time")
	}
	w := httptest.NewRecorder()
	dashboard(s).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(w.Body.String(), "Updated "+r.UpdatedAt.Format("15:04:05 UTC")) {
		t.Fatal(w.Body.String())
	}
	r.Status = "healthy"
	s.apply("filesystem", []Observation{{ID: "one", Source: "filesystem", Report: &r}}, nil, now)
	healthy := httptest.NewRecorder()
	dashboard(s).ServeHTTP(healthy, httptest.NewRequest("GET", "/", nil))
	if strings.Contains(healthy.Body.String(), "<time class=\"updated\"") {
		t.Fatal("healthy service displayed update time")
	}

}
