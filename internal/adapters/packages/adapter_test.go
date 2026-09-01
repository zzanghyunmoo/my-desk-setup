package packages

import "testing"

func TestContainsExactVersionPreservesReleaseSuffix(t *testing.T) {
	output := "rust-analyzer 0.3.2989-standalone"
	if !containsExactVersion(output, "0.3.2989-standalone") {
		t.Fatal("full rust-analyzer release identity was not recognized")
	}
	if containsExactVersion(output, "0.3.2989") {
		t.Fatal("truncated rust-analyzer version was accepted as exact")
	}
}
