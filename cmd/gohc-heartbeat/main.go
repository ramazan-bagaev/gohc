// gohc-heartbeat reports local HTTP health for applications without a backend.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ramazan-bagaev/gohc/client"
)

func main() {
	service := flag.String("service", "", "application name (required)")
	url := flag.String("url", "http://127.0.0.1:80/", "local HTTP health URL")
	interval := flag.Duration("interval", time.Minute, "heartbeat interval")
	ttl := flag.Duration("ttl", 3*time.Minute, "report expiry")
	flag.Parse()
	if *service == "" {
		log.Fatal("--service is required")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	httpClient := &http.Client{Timeout: 3 * time.Second}
	reporter, err := client.Start(*service, client.Options{
		Interval: *interval, TTL: *ttl,
		OnError: func(err error) { log.Printf("gohc: %v", err) },
		Check:   func() client.Health { return checkHTTP(ctx, httpClient, *url) },
	})
	if err != nil {
		log.Fatal(err)
	}
	defer reporter.Stop()
	<-ctx.Done()
}

func checkHTTP(ctx context.Context, httpClient *http.Client, url string) client.Health {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return client.Health{Status: "unhealthy", Message: err.Error()}
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return client.Health{Status: "unhealthy", Message: err.Error()}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return client.Health{Status: "unhealthy", Message: fmt.Sprintf("HTTP %d", response.StatusCode)}
	}
	return client.Health{Status: "healthy"}
}
