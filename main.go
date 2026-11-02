package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/ramazan-bagaev/gohc/client"
	"github.com/ramazan-bagaev/gohc/report"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

var version = "dev"
var commit = "unknown"
var buildDate = "unknown"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gohc:", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		fmt.Printf("gohc %s (commit %s, built %s)\n", version, commit, buildDate)
		return nil
	}
	command := "start"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = args[0]
		args = args[1:]
	}
	if command != "start" && command != "status" {
		return fmt.Errorf("usage: gohc [start|status] [flags] | gohc version")
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	addr := flags.String("listen", "127.0.0.1:8901", "dashboard/API listen address")
	dir := flags.String("dir", report.DefaultDir(), "local report directory (defaults to GOHC_DIR or temp/gohc/reports)")
	configDir, e := os.UserConfigDir()
	if e != nil {
		return e
	}
	defaultConfig := filepath.Join(configDir, "gohc", "config.json")
	if value := os.Getenv("GOHC_CONFIG"); value != "" {
		defaultConfig = value
	}
	configPath := flags.String("config", defaultConfig, "optional monitor config JSON file")
	state := flags.String("state", filepath.Join(configDir, "gohc", "state", "observations.json"), "monitor-owned observation snapshot")
	interval := flags.Duration("interval", defaultUpdateInterval, "source poll interval")
	machineInterval := flags.Duration("machine-interval", defaultUpdateInterval, "machine sampling interval")
	refresh := flags.Duration("refresh", defaultUpdateInterval, "dashboard refresh interval")
	docker := flags.Bool("docker", true, "discover Docker containers")
	if e = flags.Parse(args); e != nil {
		if e == flag.ErrHelp {
			return nil
		}
		return e
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	var config MonitorConfig
	explicit := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
	if e = loadMonitorConfig(*configPath, explicit["config"] || os.Getenv("GOHC_CONFIG") != "", explicit, interval, machineInterval, refresh, &config); e != nil {
		return e
	}
	for name, value := range map[string]time.Duration{"interval": *interval, "machine-interval": *machineInterval, "refresh": *refresh} {
		if value < time.Second || value > 24*time.Hour {
			return fmt.Errorf("%s must be between one second and 24 hours", name)
		}
	}
	root, e := filepath.Abs(*dir)
	if e != nil {
		return e
	}
	statePath, e := filepath.Abs(*state)
	if e != nil {
		return e
	}
	rel, e := filepath.Rel(root, statePath)
	if e != nil {
		return e
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("state must be outside the scanned report directory")
	}
	remotes, e := remoteMonitors(config.Remotes, command != "status")
	if e != nil {
		return e
	}
	store := newStore()
	store.name = config.Name
	if store.name == "" {
		store.name, _ = os.Hostname()
	}
	if store.name == "" {
		store.name = "Local"
	}
	if len(store.name) > 256 {
		return fmt.Errorf("monitor name must be at most 256 bytes")
	}

	if e = store.load(statePath); e != nil {
		return fmt.Errorf("read persisted observations: %w", e)
	}
	store.configureRemotes(remotes)
	if command == "status" {
		b, e := json.MarshalIndent(store.snapshot(time.Now().UTC()), "", "  ")
		if e != nil {
			return e
		}
		fmt.Println(string(b))
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	mux := dashboard(store, *refresh)
	server := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	listener, e := net.Listen("tcp", *addr)
	if e != nil {
		return e
	}
	selfInterval := *interval
	if selfInterval > defaultUpdateInterval {
		selfInterval = defaultUpdateInterval
	}
	host, _ := os.Hostname()
	self, err := client.Start("gohc", client.Options{
		Dir: root, Instance: host + ":" + listener.Addr().String(), Interval: selfInterval, TTL: 3 * selfInterval,
		Check:   func() client.Health { return store.selfHealth(time.Now().UTC(), *interval) },
		OnError: func(err error) { log.Printf("self heartbeat: %v", err) },
	})
	if err != nil {
		listener.Close()
		return fmt.Errorf("start self heartbeat: %w", err)
	}
	defer self.Stop()
	machineDone := make(chan struct{})
	go func() { defer close(machineDone); machineLoop(ctx, store, root, *machineInterval) }()
	done := make(chan struct{})
	go func() { defer close(done); poll(ctx, store, root, statePath, *docker, *interval, remotes...) }()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	log.Printf("dashboard http://%s | reports %s | state %s", listener.Addr(), root, statePath)
	e = server.Serve(listener)
	stop()
	<-done
	<-machineDone
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
func poll(ctx context.Context, s *Store, dir, state string, useDocker bool, interval time.Duration, remotes ...remoteMonitor) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var dc *dockerClient
	defer func() {
		if dc != nil {
			dc.client.CloseIdleConnections()
		}
	}()
	for {
		now := time.Now().UTC()
		files, err := scanFiles(dir, now)
		s.apply("filesystem", files, err, now)
		if useDocker {
			err = nil
			if dc == nil {
				dc, err = newDockerClient()
			}
			var entries []Observation
			if err == nil {
				scan, cancel := context.WithTimeout(ctx, 30*time.Second)
				entries, err = dc.scan(scan, now)
				cancel()
			}
			s.apply("docker", entries, err, now)
			if err != nil && dc != nil {
				dc.client.CloseIdleConnections()
				dc = nil
			}
		}
		s.collectRemotes(ctx, remotes)
		if err := s.save(state); err != nil {
			log.Printf("save observations: %v", err)
		}
		s.mu.Lock()
		s.lastPoll = time.Now().UTC()
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type dashboardRow struct {
	Origin        string
	Containerized bool
	UpdatedAt     string
	UpdatedLabel  string
	UpdatedFull   string
	Name          string
	Status        string
	Tone          string
	Rank          int
}
type dashboardMetric struct {
	Name  string
	Value string
}
type dashboardGroup struct {
	Name     string
	Rows     []dashboardRow
	Machine  []dashboardMetric
	Problems []string
}
type dashboardView struct {
	Groups         []dashboardGroup
	RefreshSeconds int
	Machine        []dashboardMetric
	Rows           []dashboardRow
	Problems       []string
}

func dashboardData(snapshot Snapshot, refresh ...time.Duration) dashboardView {
	duration := defaultUpdateInterval
	if len(refresh) > 0 {
		duration = refresh[0]
	}
	view := dashboardView{Rows: []dashboardRow{}, RefreshSeconds: int((duration + time.Second - 1) / time.Second)}
	if m := snapshot.Machine; m != nil {
		memory, disk := "—", "—"
		if m.Memory != nil {
			memory = metricPercent(&m.Memory.Percent)
		}
		if m.Disk != nil {
			disk = metricPercent(&m.Disk.Percent)
		}
		view.Machine = []dashboardMetric{{"CPU", metricPercent(m.CPUPercent)}, {"Memory", memory}, {"Disk", disk}}
	}
	for _, v := range snapshot.Observations {
		if v.Status == "reporting" {
			continue
		}
		name := strings.TrimPrefix(v.ContainerName, "/")
		if v.Report != nil {
			name = v.Report.Service
		}
		if name == "" {
			name = "Unreadable report"
		}
		row := dashboardRow{Origin: v.Origin, Name: name, Status: "Unknown", Tone: "unknown", Rank: 3, Containerized: (v.Source == "docker" || v.OriginSource == "docker") && v.ContainerID != ""}
		if v.Report != nil {
			updated := v.Report.UpdatedAt.UTC()
			row.UpdatedAt = updated.Format(time.RFC3339Nano)
			row.UpdatedLabel = updated.Format("15:04:05 UTC")
			row.UpdatedFull = updated.Format("2006-01-02 15:04:05 UTC")
		}
		switch v.Status {
		case "healthy":
			row.Status = "Healthy"
			row.Tone = "healthy"
			row.Rank = 2
		case "unhealthy":
			row.Status = "Unhealthy"
			row.Tone = "unhealthy"
			row.Rank = 0
		case "stale":
			row.Status = "Stale"
			row.Tone = "stale"
			row.Rank = 1
		case "stopped", "exited", "dead":
			row.Status = "Stopped"
			row.Tone = "unknown"
			row.Rank = 3
		case "source_unavailable":
			row.Status = "Unavailable"
			row.Rank = 0
		case "missing":
			row.Status = "Missing"
			row.Rank = 1
		case "awaiting_verification":
			row.Status = "Checking"
		case "invalid":
			row.Status = "Invalid"
			row.Rank = 0
		case "paused":
			row.Status = "Paused"
			row.Tone = "unknown"
			row.Rank = 3
		}
		view.Rows = append(view.Rows, row)
	}
	sort.SliceStable(view.Rows, func(i, j int) bool {
		if view.Rows[i].Rank != view.Rows[j].Rank {
			return view.Rows[i].Rank < view.Rows[j].Rank
		}
		return view.Rows[i].Name < view.Rows[j].Name
	})
	if state, ok := snapshot.Sources["docker"]; ok && state.Error != "" {
		view.Problems = append(view.Problems, "Docker unavailable")
	}
	if state, ok := snapshot.Sources["filesystem"]; ok && state.Error != "" {
		view.Problems = append(view.Problems, "Reports unavailable")
	}
	if snapshot.PersistenceError != "" {
		view.Problems = append(view.Problems, "Could not save status")
	}
	var names []string
	for source := range snapshot.Sources {
		if strings.HasPrefix(source, "remote:") {
			names = append(names, strings.TrimPrefix(source, "remote:"))
		}
	}
	sort.Strings(names)
	if len(names) > 0 {
		local := dashboardGroup{Name: snapshot.Monitor, Machine: view.Machine, Problems: view.Problems}
		if local.Name == "" {
			local.Name = "Local"
		}
		for _, row := range view.Rows {
			if row.Origin == "" {
				local.Rows = append(local.Rows, row)
			}
		}
		view.Groups = append(view.Groups, local)
		for _, name := range names {
			state := snapshot.Sources["remote:"+name]
			group := dashboardGroup{Name: name}
			for _, row := range view.Rows {
				if row.Origin == name {
					group.Rows = append(group.Rows, row)
				}
			}
			if state.Error != "" {
				group.Problems = []string{"Monitor unavailable"}
			} else if state.Machine != nil {
				group.Machine = dashboardData(Snapshot{Machine: state.Machine}).Machine
			}
			view.Groups = append(view.Groups, group)
		}
	}
	return view
}

var page = template.Must(template.New("dashboard").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta http-equiv="refresh" content="{{.RefreshSeconds}}">
<title>gohc</title>
<style>
:root{font-family:Inter,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#252a30;background:#fafafa;font-synthesis:none}
*{box-sizing:border-box}
body{margin:0;padding:48px 24px}
main{max-width:720px;margin:0 auto}
h2{font-size:14px;font-weight:600;margin:28px 0 16px}section:first-of-type h2{margin-top:0}
h1{font-size:22px;font-weight:650;letter-spacing:-.7px;margin:0 0 32px}
.machine{display:grid;grid-template-columns:repeat(3,1fr);gap:20px;padding:0 20px;margin:0 0 24px}
.metric{display:flex;align-items:baseline;gap:10px}
.metric-label{font-size:12px;color:#858a91}
.metric-value{font-size:16px;font-weight:500;font-variant-numeric:tabular-nums}
.services{background:#fff;border:1px solid #e7e8eb;border-radius:12px;overflow:hidden}
.service{display:flex;align-items:center;justify-content:space-between;gap:24px;min-height:46px;padding:12px 20px;border-bottom:1px solid #eff0f2}
.service:last-child{border-bottom:0}
.health{display:flex;flex-direction:column;align-items:flex-end;gap:5px;flex-shrink:0}
.updated{font-size:11px;color:#90959d;font-variant-numeric:tabular-nums}
.service-name{display:flex;align-items:center;gap:8px;min-width:0}
.container-kind{font-size:10px;font-weight:500;color:#87919c;border:1px solid #e4e8ed;border-radius:4px;padding:2px 5px;white-space:nowrap}
.name{font-size:14px;font-weight:500;overflow-wrap:anywhere;min-width:0}
.status{display:inline-flex;align-items:center;gap:8px;flex-shrink:0;font-size:12px;color:#777e87}
.dot{width:6px;height:6px;border-radius:50%;background:#c2c6cc}
.healthy{color:#47775c}.healthy .dot{background:#58a277}
.unhealthy{color:#b34d4d}.unhealthy .dot{background:#d76a6a}
.stale{color:#a87c38}.stale .dot{background:#d3a353}
.problem{font-size:13px;color:#a65c3c;margin:0 0 16px}
.empty{font-size:14px;color:#777e87;padding:24px 20px;margin:0}
@media(max-width:480px){body{padding:28px 18px}h1{margin-bottom:24px}.service{padding:12px 16px;gap:16px}}
</style>
</head>
<body><main>
<h1>gohc</h1>
{{if .Groups}}{{range .Groups}}<section><h2>{{.Name}}</h2>{{template "services" .}}</section>{{end}}{{else}}{{template "services" .}}{{end}}
</main>
<script>
for (const element of document.querySelectorAll("time.updated")) {
  const date = new Date(element.dateTime);
  if (!Number.isNaN(date.getTime())) {
    element.textContent = "Updated " + new Intl.DateTimeFormat(undefined, {hour:"2-digit",minute:"2-digit",second:"2-digit"}).format(date);
    element.title = "Last report: " + date.toLocaleString(undefined, {timeZoneName:"short"});
  }
}
</script>
</body></html>
{{define "services"}}{{if .Machine}}<div class="machine">{{range .Machine}}<div class="metric"><span class="metric-label">{{.Name}}</span><span class="metric-value">{{.Value}}</span></div>{{end}}</div>{{end}}
{{range .Problems}}<p class="problem">{{.}}</p>{{end}}
<div class="services">
{{range .Rows}}<div class="service"><div class="service-name"><span class="name">{{.Name}}</span>{{if .Containerized}}<span class="container-kind" title="Discovered through Docker">Docker</span>{{end}}</div><div class="health"><span class="status {{.Tone}}"><span class="dot" aria-hidden="true"></span>{{.Status}}</span>{{if and (ne .Status "Healthy") .UpdatedAt}}<time class="updated" datetime="{{.UpdatedAt}}" title="Last report: {{.UpdatedFull}}">Updated {{.UpdatedLabel}}</time>{{end}}</div></div>
{{else}}<p class="empty">No services yet</p>{{end}}
</div>{{end}}`))

func dashboard(s *Store, refresh ...time.Duration) *http.ServeMux {
	mux := http.NewServeMux()
	for _, endpoint := range []string{"/api/status", "/api/local"} {
		local := endpoint == "/api/local"
		mux.HandleFunc(endpoint, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" {
				w.WriteHeader(405)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			snapshot := s.snapshot(time.Now().UTC())
			if local {
				snapshot = s.localSnapshot(time.Now().UTC())
			}
			json.NewEncoder(w).Encode(snapshot)
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		page.Execute(w, dashboardData(s.snapshot(time.Now().UTC()), refresh...))
	})
	return mux
}
