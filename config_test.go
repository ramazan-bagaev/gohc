package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTimingConfigurationAndFlagPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"poll_interval":"2m","machine_interval":"3m","dashboard_refresh":"90s"}`), 0600)
	poll, machine, refresh := 10*time.Second, time.Minute, time.Minute
	if e := loadTimingConfig(path, true, map[string]bool{"interval": true}, &poll, &machine, &refresh); e != nil {
		t.Fatal(e)
	}
	if poll != 10*time.Second || machine != 3*time.Minute || refresh != 90*time.Second {
		t.Fatal(poll, machine, refresh)
	}
}
func TestTimingDefaultsDoNotNeedFile(t *testing.T) {
	poll, machine, refresh := defaultUpdateInterval, defaultUpdateInterval, defaultUpdateInterval
	path := filepath.Join(t.TempDir(), "missing.json")
	if e := loadTimingConfig(path, false, nil, &poll, &machine, &refresh); e != nil {
		t.Fatal(e)
	}
	if e := loadTimingConfig(path, true, nil, &poll, &machine, &refresh); e == nil {
		t.Fatal("explicit missing config silently ignored")
	}
	if view := dashboardData(Snapshot{}); view.RefreshSeconds != 60 {
		t.Fatal(view)
	}
	if view := dashboardData(Snapshot{}, 90*time.Second); view.RefreshSeconds != 90 {
		t.Fatal(view)
	}
}
func TestTimingConfigRejectsMistakes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for _, data := range []string{`{"poll_intervl":"1m"}`, `{"poll_interval":"0s"}`, `{"poll_interval":"25h"}`, `{"machine_interval":"frequently"}`, `{} {}`, `{`} {
		os.WriteFile(path, []byte(data), 0600)
		poll, machine, refresh := time.Minute, time.Minute, time.Minute
		if e := loadTimingConfig(path, true, nil, &poll, &machine, &refresh); e == nil {
			t.Fatal("accepted", data)
		}
	}
}
func TestTimingFlagsRejectNonpositiveSampling(t *testing.T) {
	if e := run([]string{"start", "--machine-interval", "0s"}); e == nil || !strings.Contains(e.Error(), "machine-interval") {
		t.Fatal(e)
	}
}
