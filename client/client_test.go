package client

import (
	"github.com/ramazan-bagaev/gohc/report"
	"os"
	"testing"
	"time"
)

func TestImmediateReportAndShutdown(t *testing.T) {
	r, e := Start("../../service", Options{Dir: t.TempDir(), Check: func() Health { return Health{Status: "unhealthy", Message: "database unavailable"} }})
	if e != nil {
		t.Fatal(e)
	}
	read := func() report.Report {
		f, e := os.Open(r.Path)
		if e != nil {
			t.Fatal(e)
		}
		defer f.Close()
		v, e := report.Read(f, time.Now())
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	if v := read(); v.Status != "unhealthy" || v.Service != "../../service" {
		t.Fatalf("%+v", v)
	}
	r.Stop()
	r.Stop()
	if v := read(); v.Status != "stopped" {
		t.Fatal(v)
	}
}
func TestHeartbeatAdvancesAndPanicBecomesUnhealthy(t *testing.T) {
	r, e := Start("test", Options{Dir: t.TempDir(), Interval: 5 * time.Millisecond, TTL: time.Second, Check: func() Health { panic("oops") }})
	if e != nil {
		t.Fatal(e)
	}
	defer r.Stop()
	first, _ := os.ReadFile(r.Path)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
		b, _ := os.ReadFile(r.Path)
		if string(b) != string(first) {
			f, _ := os.Open(r.Path)
			v, e := report.Read(f, time.Now())
			f.Close()
			if e != nil || v.Status != "unhealthy" {
				t.Fatal(v, e)
			}
			return
		}
	}
	t.Fatal("heartbeat did not advance")
}
func TestInitializationReportsWriteFailure(t *testing.T) {
	dir := t.TempDir() + "/file"
	os.WriteFile(dir, []byte("file"), 0600)
	if _, e := Start("test", Options{Dir: dir}); e == nil {
		t.Fatal("expected failure")
	}
}

func TestExplicitInstanceReusesReportOnRestart(t *testing.T) {
	opts := Options{Dir: t.TempDir(), Instance: "stable-monitor"}
	a, e := Start("gohc", opts)
	if e != nil {
		t.Fatal(e)
	}
	path := a.Path
	a.Stop()
	b, e := Start("gohc", opts)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Stop()
	if b.Path != path {
		t.Fatal("same instance created duplicate report")
	}
	entries, e := os.ReadDir(opts.Dir)
	if e != nil || len(entries) != 1 {
		t.Fatal(entries, e)
	}
}
