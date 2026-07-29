package version

import "testing"

func TestString(t *testing.T) {
	originalVersion, originalCommit, originalDate := Version, Commit, Date
	t.Cleanup(func() {
		Version, Commit, Date = originalVersion, originalCommit, originalDate
	})

	Version = "v0.1.0"
	Commit = "abc123"
	Date = "2026-07-29"

	if got, want := String(), "v0.1.0 (commit=abc123, date=2026-07-29)"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}
