// Package report defines the language-independent gohc heartbeat protocol.
package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const MaxBytes = 64 * 1024

// Report is version 1 of the on-disk protocol. UpdatedAt must change on every
// heartbeat; rereading a report never renews its expiry.
type Report struct {
	Version    int       `json:"version"`
	Service    string    `json:"service"`
	Instance   string    `json:"instance"`
	Status     string    `json:"status"`
	Message    string    `json:"message,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
	TTLSeconds int       `json:"ttl_seconds"`
}

// DefaultDir does not depend on container detection or the current user.
// /tmp is intentionally used so ordinary non-root applications can write.
func DefaultDir() string {
	if p := os.Getenv("GOHC_DIR"); p != "" {
		return p
	}
	return filepath.Join(os.TempDir(), "gohc", "reports")
}

func (r Report) Validate(now time.Time) error {
	if r.Version != 1 {
		return errors.New("unsupported report version")
	}
	if r.Service == "" || r.Instance == "" || len(r.Service) > 256 || len(r.Instance) > 256 {
		return errors.New("service and instance must contain 1–256 bytes")
	}
	switch r.Status {
	case "healthy", "unhealthy", "unknown", "stopped":
	default:
		return fmt.Errorf("invalid status %q", r.Status)
	}
	if r.UpdatedAt.IsZero() || r.UpdatedAt.After(now.Add(5*time.Minute)) {
		return errors.New("missing or excessively future updated_at")
	}
	if r.TTLSeconds < 1 || r.TTLSeconds > 86400 {
		return errors.New("ttl_seconds must be between 1 and 86400")
	}
	return nil
}

func Read(reader io.Reader, now time.Time) (Report, error) {
	var r Report
	b, err := io.ReadAll(io.LimitReader(reader, MaxBytes+1))
	if err != nil {
		return r, err
	}
	if len(b)+1 > MaxBytes {
		return r, errors.New("report exceeds 64 KiB")
	}
	if err = json.Unmarshal(b, &r); err != nil {
		return r, err
	}
	return r, r.Validate(now)
}

// Write atomically replaces a report using a temporary file in the same directory.
func Write(path string, r Report) error {
	if err := r.Validate(time.Now()); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(b)+1 > MaxBytes {
		return errors.New("report exceeds 64 KiB")
	}
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".gohc-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Chmod(f.Name(), 0644); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
