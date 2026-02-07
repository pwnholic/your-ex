package analyzer

import (
	"testing"
	"time"
)

// Helper functions for tests

func stringPtr(s string) *string {
	return &s
}

func durationPtr(d time.Duration) *time.Duration {
	return &d
}

func requireNoError(t *testing.T, err error, msgAndArgs ...any) {
	if err != nil {
		t.Helper()
		t.Fatalf("Error is not nil: %v %v", err, msgAndArgs)
	}
}
