package store

import (
	"testing"
	"time"
)

func TestBackoffIsCapped(t *testing.T) {
	if got := Backoff(1); got != time.Second {
		t.Fatalf("Backoff(1) = %s", got)
	}
	if got := Backoff(50); got != 64*time.Second {
		t.Fatalf("Backoff(50) = %s, want 64s", got)
	}
}
