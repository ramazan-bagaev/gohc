package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMonitorConfigurationAndFlagPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"poll_interval":"2m","machine_interval":"3m","dashboard_refresh":"90s"}`), 0600)
	poll, machine, refresh := 10*time.Second, time.Minute, time.Minute
	if e := loadMonitorConfig(path, true, map[string]bool{"interval": true}, &poll, &machine, &refresh); e != nil {
		t.Fatal(e)
	}
	if poll != 10*time.Second || machine != 3*time.Minute || refresh != 90*time.Second {
		t.Fatal(poll, machine, refresh)
	}
}
func TestTimingDefaultsDoNotNeedFile(t *testing.T) {
	poll, machine, refresh := defaultUpdateInterval, defaultUpdateInterval, defaultUpdateInterval
	path := filepath.Join(t.TempDir(), "missing.json")
	if e := loadMonitorConfig(path, false, nil, &poll, &machine, &refresh); e != nil {
		t.Fatal(e)
	}
	if e := loadMonitorConfig(path, true, nil, &poll, &machine, &refresh); e == nil {
		t.Fatal("explicit missing config silently ignored")
	}
	if view := dashboardData(Snapshot{}); view.RefreshSeconds != 60 {
		t.Fatal(view)
	}
	if view := dashboardData(Snapshot{}, 90*time.Second); view.RefreshSeconds != 90 {
		t.Fatal(view)
	}
}
func TestMonitorConfigRejectsMistakes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for _, data := range []string{`{"poll_intervl":"1m"}`, `{"poll_interval":"0s"}`, `{"poll_interval":"25h"}`, `{"machine_interval":"frequently"}`, `{} {}`, `{`} {
		os.WriteFile(path, []byte(data), 0600)
		poll, machine, refresh := time.Minute, time.Minute, time.Minute
		if e := loadMonitorConfig(path, true, nil, &poll, &machine, &refresh); e == nil {
			t.Fatal("accepted", data)
		}
	}
}
func TestTimingFlagsRejectNonpositiveSampling(t *testing.T) {
	if e := run([]string{"start", "--machine-interval", "0s"}); e == nil || !strings.Contains(e.Error(), "machine-interval") {
		t.Fatal(e)
	}
}

func TestMonitorConfigLoadsRemoteEndpoints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"name":"primary","remotes":[{"name":"peer","url":"https://peer.example.com","username":"reader","password_env":"REMOTE_PASSWORD"}]}`), 0600)
	var config MonitorConfig
	poll, machine, refresh := time.Minute, time.Minute, time.Minute
	if err := loadMonitorConfig(path, true, nil, &poll, &machine, &refresh, &config); err != nil {
		t.Fatal(err)
	}
	if config.Name != "primary" || len(config.Remotes) != 1 || config.Remotes[0].PasswordEnv != "REMOTE_PASSWORD" {
		t.Fatalf("%+v", config)
	}
}
