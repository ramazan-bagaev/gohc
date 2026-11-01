package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProtocolRejectsInvalidAndOversizedInput(t *testing.T) {
	now := time.Now().UTC()
	for _, s := range []string{`{}`, `{"version":2}`, strings.Repeat(" ", MaxBytes+1), `{"version":1,"service":"a","instance":"b","status":"healthy","updated_at":"2099-01-01T00:00:00Z","ttl_seconds":60}`} {
		if _, e := Read(strings.NewReader(s), now); e == nil {
			t.Fatal("accepted invalid report")
		}
	}
}

func TestJavaInteroperability(t *testing.T) {
	dir := os.Getenv("GOHC_JAVA_FIXTURES")
	if dir == "" {
		t.Skip("run make test-java for Java interoperability")
	}
	root, e := filepath.Abs(filepath.Join("..", dir))
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"java-initial.fixture", "java-final.fixture"} {
		f, e := os.Open(filepath.Join(root, name))
		if e != nil {
			t.Fatal(e)
		}
		r, e := Read(f, time.Now())
		f.Close()
		if e != nil {
			t.Fatal(e)
		}
		if r.Service != "java-smoke" || r.Instance != "java-instance" {
			t.Fatalf("%+v", r)
		}
		if name == "java-initial.fixture" && (r.Status != "unhealthy" || r.Message != "database \"offline\"\ncheck \\ path\t雪") {
			t.Fatalf("%+v", r)
		}
		if name == "java-final.fixture" && r.Status != "stopped" {
			t.Fatalf("%+v", r)
		}
	}
}
