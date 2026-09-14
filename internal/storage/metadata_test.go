package storage

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNewJobID_Format(t *testing.T) {
	now := time.Date(2026, 7, 14, 15, 30, 45, 0, time.UTC)
	id := NewJobID(now)
	if !strings.HasPrefix(id, "20260714-153045-") {
		t.Errorf("got %q, want prefix 20260714-153045-", id)
	}
	if len(id) != len("20260714-153045-")+4 {
		t.Errorf("unexpected length: %q", id)
	}
}

func TestNewJobID_Uniqueness(t *testing.T) {
	// NewJobID's suffix is only 4 hex characters (65536 possibilities), so
	// drawing real crypto/rand output here would make this test genuinely
	// flaky: 50 real random draws collide by the birthday paradox roughly
	// 2% of the time, unrelated to any actual bug (a doxie-scanner
	// realistically never starts 50 jobs in the same clock second).
	// Stubbing cryptoRandRead with a counter instead tests the real
	// property this function needs — distinct random bytes map to
	// distinct IDs — deterministically, with no dependence on chance.
	original := cryptoRandRead
	var counter byte
	cryptoRandRead = func(b []byte) (int, error) {
		for i := range b {
			counter++
			b[i] = counter
		}
		return len(b), nil
	}
	defer func() { cryptoRandRead = original }()

	now := time.Date(2026, 7, 14, 15, 30, 45, 0, time.UTC)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		id := NewJobID(now)
		if seen[id] {
			t.Fatalf("duplicate job ID generated: %q", id)
		}
		seen[id] = true
	}
}

func TestNewJobName(t *testing.T) {
	now := time.Date(2026, 7, 14, 15, 30, 0, 0, time.UTC)
	got := NewJobName(now)
	want := "Scan 2026-07-14 15:30"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRandHex_PanicsWhenCryptoRandFails(t *testing.T) {
	original := cryptoRandRead
	cryptoRandRead = func(b []byte) (int, error) { return 0, errors.New("simulated entropy failure") }
	defer func() { cryptoRandRead = original }()

	defer func() {
		if recover() == nil {
			t.Fatal("expected a panic")
		}
	}()
	randHex(4)
}

func TestPageFilenames(t *testing.T) {
	if got := PageFilename(1); got != "page-001.png" {
		t.Errorf("PageFilename(1): got %q", got)
	}
	if got := PageFilename(42); got != "page-042.png" {
		t.Errorf("PageFilename(42): got %q", got)
	}
}
