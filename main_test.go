package main

import (
	"testing"
)

func TestGetStatuses(t *testing.T) {
	statuses := GetStatuses([]string{"test", "//self"})

	if statuses != "test-app\ntest-gohc\nok\n" {
		t.Fatalf("wrong statuses:\n%s", statuses)
	}
}
