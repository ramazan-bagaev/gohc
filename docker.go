package main

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ramazan-bagaev/gohc/report"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type dockerClient struct {
	client         *http.Client
	base, identity string
}
type dockerContainer struct {
	ID    string   `json:"Id"`
	Names []string `json:"Names"`
	State string   `json:"State"`
}
type dockerInfo struct {
	Config struct {
		Env []string `json:"Env"`
	} `json:"Config"`
	State struct {
		Status string `json:"Status"`
		Health *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
}

func dockerEndpoint() (endpoint string, skipTLS bool, tlsDir string, err error) {
	home, e := os.UserHomeDir()
	if e != nil {
		err = e
		return
	}
	config := os.Getenv("DOCKER_CONFIG")
	if config == "" {
		config = filepath.Join(home, ".docker")
	}
	ctx := os.Getenv("DOCKER_CONTEXT")
	if ctx == "" {
		if host := os.Getenv("DOCKER_HOST"); host != "" {
			certPath := os.Getenv("DOCKER_CERT_PATH")
			if certPath == "" && (os.Getenv("DOCKER_TLS_VERIFY") != "" || os.Getenv("DOCKER_TLS") == "1") {
				certPath = config
			}
			return host, os.Getenv("DOCKER_TLS_VERIFY") == "", certPath, nil
		}
		b, e := os.ReadFile(filepath.Join(config, "config.json"))
		if e == nil {
			var c struct {
				CurrentContext string `json:"currentContext"`
			}
			if e = json.Unmarshal(b, &c); e != nil {
				return "", false, "", e
			}
			ctx = c.CurrentContext
		} else if !os.IsNotExist(e) {
			return "", false, "", e
		}
	}
	if ctx == "" || ctx == "default" {
		return "unix:///var/run/docker.sock", true, "", nil
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(ctx)))
	b, e := os.ReadFile(filepath.Join(config, "contexts", "meta", hash, "meta.json"))
	if e != nil {
		return "", false, "", fmt.Errorf("Docker context %q: %w", ctx, e)
	}
	var c struct {
		Endpoints map[string]struct {
			Host          string `json:"Host"`
			SkipTLSVerify bool   `json:"SkipTLSVerify"`
		} `json:"Endpoints"`
	}
	if e = json.Unmarshal(b, &c); e != nil {
		return "", false, "", e
	}
	ep, ok := c.Endpoints["docker"]
	if !ok {
		return "", false, "", errors.New("context has no Docker endpoint")
	}
	return ep.Host, ep.SkipTLSVerify, filepath.Join(config, "contexts", "tls", hash, "docker"), nil
}
func newDockerClient() (*dockerClient, error) {
	endpoint, skip, tlsDir, err := dockerEndpoint()
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{}
	base := "http://docker"
	switch u.Scheme {
	case "unix":
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", u.Path)
		}
	case "tcp", "http", "https":
		scheme := "http"
		if u.Scheme == "https" || !skip || os.Getenv("DOCKER_TLS") == "1" || os.Getenv("DOCKER_TLS_VERIFY") != "" {
			scheme = "https"
		}
		base = scheme + "://" + u.Host
		if scheme == "https" {
			tc := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: skip}
			if tlsDir != "" {
				ca, e := os.ReadFile(filepath.Join(tlsDir, "ca.pem"))
				if e == nil {
					pool := x509.NewCertPool()
					if !pool.AppendCertsFromPEM(ca) {
						return nil, errors.New("invalid Docker TLS CA")
					}
					tc.RootCAs = pool
				} else if !os.IsNotExist(e) {
					return nil, e
				}
				cert, e := tls.LoadX509KeyPair(filepath.Join(tlsDir, "cert.pem"), filepath.Join(tlsDir, "key.pem"))
				if e == nil {
					tc.Certificates = []tls.Certificate{cert}
				} else if !os.IsNotExist(e) {
					return nil, e
				}
			}
			transport.TLSClientConfig = tc
		}
	default:
		return nil, fmt.Errorf("unsupported Docker endpoint scheme %q; unix and TCP/TLS contexts are supported", u.Scheme)
	}
	return &dockerClient{client: &http.Client{Transport: transport, Timeout: 5 * time.Second}, base: base, identity: fmt.Sprintf("%x", sha256.Sum256([]byte(endpoint)))[:16]}, nil
}
func (d *dockerClient) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", d.base+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("Docker HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return resp, nil
}
func (d *dockerClient) decode(ctx context.Context, path string, out interface{}) error {
	resp, err := d.get(ctx, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, 8*1024*1024)).Decode(out)
}
func dockerReportDir(env []string) string {
	// Match the clients' environment precedence. Never use the monitor's GOHC_DIR.
	dir := ""
	tmp := "/tmp"
	for _, e := range env {
		if strings.HasPrefix(e, "GOHC_DIR=") {
			dir = strings.TrimPrefix(e, "GOHC_DIR=")
		}
		if strings.HasPrefix(e, "TMPDIR=") && strings.TrimPrefix(e, "TMPDIR=") != "" {
			tmp = strings.TrimPrefix(e, "TMPDIR=")
		}
	}
	if dir != "" {
		return dir
	}
	return strings.TrimRight(tmp, "/") + "/gohc/reports"
}
func (d *dockerClient) scan(ctx context.Context, now time.Time) ([]Observation, error) {
	var containers []dockerContainer
	if err := d.decode(ctx, "/containers/json?all=1", &containers); err != nil {
		return nil, err
	}
	entries := []Observation{}
	for _, c := range containers {
		id := "docker:" + d.identity + ":" + c.ID
		name := strings.Join(c.Names, ",")
		v := Observation{ID: id, Source: "docker", Location: c.ID, ContainerID: c.ID, ContainerName: name, ContainerState: c.State}
		var info dockerInfo
		if err := d.decode(ctx, "/containers/"+url.PathEscape(c.ID)+"/json", &info); err != nil {
			v.Error = err.Error()
			entries = append(entries, v)
			continue
		}
		v.ContainerState = info.State.Status
		if info.State.Health != nil {
			v.DockerHealth = info.State.Health.Status
		}
		entries = append(entries, v)
		parentIndex := len(entries) - 1
		if v.ContainerState != "running" {
			continue
		}
		dir := dockerReportDir(info.Config.Env)
		resp, err := d.get(ctx, "/containers/"+url.PathEscape(c.ID)+"/archive?path="+url.QueryEscape(dir))
		if err != nil {
			// Missing directory is expected for containers without a client.
			if !strings.HasPrefix(err.Error(), "Docker HTTP 404:") {
				entries[parentIndex].Error = err.Error()
			}
			continue
		}
		// Bound work on unexpected or malicious directory contents, and never extract tar paths.
		tr := tar.NewReader(io.LimitReader(resp.Body, 4*1024*1024))
		count := 0
		for {
			h, e := tr.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				entries[parentIndex].Error = e.Error()
				break
			}
			count++
			if count > 1024 {
				entries[parentIndex].Error = "too many report archive entries"
				break
			}
			if h.Typeflag != tar.TypeReg || !strings.HasSuffix(h.Name, ".json") {
				continue
			}
			item := v
			item.ID = id + ":" + h.Name
			item.Location = path.Join(dir, strings.TrimPrefix(h.Name, path.Base(path.Clean(dir))+"/"))
			r, e := report.Read(tr, now)
			if e != nil {
				item.Error = e.Error()
			} else {
				item.Report = &r
			}
			entries = append(entries, item)
		}
		resp.Body.Close()
	}
	return entries, nil
}
