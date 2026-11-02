package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPHealth(t *testing.T) {
	for _, status := range []int{200, 204, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
			defer server.Close()
			health := checkHTTP(context.Background(), server.Client(), server.URL)
			want := "healthy"
			if status >= 400 {
				want = "unhealthy"
			}
			if health.Status != want {
				t.Fatalf("got %s; want %s", health.Status, want)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if health := checkHTTP(ctx, http.DefaultClient, "http://127.0.0.1:1"); health.Status != "unhealthy" {
		t.Fatal("cancelled/unreachable HTTP probe must be unhealthy")
	}
}
