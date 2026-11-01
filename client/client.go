// Package client writes lightweight gohc reports without any knowledge of Docker.
package client

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/ramazan-bagaev/gohc/report"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Health struct {
	Status  string
	Message string
}
type Options struct {
	Dir      string
	Instance string
	Interval time.Duration
	TTL      time.Duration
	// Check should return promptly. It runs immediately and on each heartbeat.
	Check func() Health
	// OnError receives write failures; heartbeat failures do not terminate the app.
	OnError func(error)
}
type Reporter struct {
	stop chan struct{}
	done chan struct{}
	once sync.Once
	Path string
}

// Start publishes the first report synchronously, so initialization errors are visible.
func Start(service string, opts Options) (*Reporter, error) {
	notify := func(err error) {
		if opts.OnError != nil {
			func() { defer func() { recover() }(); opts.OnError(err) }()
		}
	}
	if opts.Dir == "" {
		opts.Dir = report.DefaultDir()
	}
	if opts.Interval == 0 {
		opts.Interval = time.Minute
	}
	if opts.TTL == 0 {
		opts.TTL = 3 * opts.Interval
	}
	if opts.Interval < time.Millisecond || opts.TTL < time.Second || opts.TTL > 24*time.Hour || opts.TTL <= opts.Interval {
		return nil, errors.New("TTL must exceed a positive interval and be between one second and 24 hours")
	}
	if opts.Instance == "" {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		host, _ := os.Hostname()
		opts.Instance = fmt.Sprintf("%s-%d-%s", host, os.Getpid(), hex.EncodeToString(b))
	}
	// Opaque filenames avoid path traversal and collisions between arbitrary names.
	// Explicit instance identities reuse their file across restarts. Generated
	// instance IDs remain unique, and all filenames stay opaque and path-safe.
	digest := sha256.Sum256([]byte(service + "\x00" + opts.Instance))
	p := filepath.Join(opts.Dir, hex.EncodeToString(digest[:])+".json")
	write := func(status string) error {
		h := Health{Status: "healthy"}
		if opts.Check != nil && status == "" {
			func() {
				defer func() {
					if v := recover(); v != nil {
						h = Health{Status: "unhealthy", Message: fmt.Sprintf("health callback panicked: %v", v)}
					}
				}()
				h = opts.Check()
			}()
		}
		if status != "" {
			h = Health{Status: status}
		}
		return report.Write(p, report.Report{Version: 1, Service: service, Instance: opts.Instance, Status: h.Status, Message: h.Message, UpdatedAt: time.Now().UTC(), TTLSeconds: int((opts.TTL + time.Second - 1) / time.Second)})
	}
	if err := write(""); err != nil {
		return nil, err
	}
	r := &Reporter{stop: make(chan struct{}), done: make(chan struct{}), Path: p}
	go func() {
		defer close(r.done)
		t := time.NewTicker(opts.Interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if err := write(""); err != nil {
					notify(err)
				}
			case <-r.stop:
				if err := write("stopped"); err != nil {
					notify(err)
				}
				return
			}
		}
	}()
	return r, nil
}
func (r *Reporter) Stop() { r.once.Do(func() { close(r.stop) }); <-r.done }
