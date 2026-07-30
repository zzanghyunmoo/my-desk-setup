package guest

import (
	"strings"
	"testing"
)

func TestOwnershipRequiresPreparingToCommittedTransition(t *testing.T) {
	root := t.TempDir()
	record := Ownership{
		Provider:    "lima",
		Name:        "mds",
		ImageURL:    "https://example.invalid/ubuntu.img",
		ImageSHA256: strings.Repeat("a", 64),
	}
	var err error
	record, err = PrepareOwnership(root, record)
	if err != nil {
		t.Fatalf("PrepareOwnership(): %v", err)
	}
	if len(record.CreationNonce) != 64 {
		t.Fatalf("creation nonce length = %d, want 64", len(record.CreationNonce))
	}
	preparing, exists, err := LoadOwnership(root, "lima", "mds")
	if err != nil || !exists || preparing.Phase != OwnershipPreparing {
		t.Fatalf(
			"preparing ownership = %+v exists=%t error=%v",
			preparing,
			exists,
			err,
		)
	}
	if err := CommitOwnership(root, record); err != nil {
		t.Fatalf("CommitOwnership(): %v", err)
	}
	committed, exists, err := LoadOwnership(root, "lima", "mds")
	if err != nil || !exists || committed.Phase != OwnershipCommitted {
		t.Fatalf(
			"committed ownership = %+v exists=%t error=%v",
			committed,
			exists,
			err,
		)
	}
	if err := CommitOwnership(root, record); err == nil {
		t.Fatal("CommitOwnership() accepted an already committed record")
	}
}
