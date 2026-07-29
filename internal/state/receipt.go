package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Receipt struct {
	SchemaVersion   string          `json:"schema_version"`
	PlanDigest      string          `json:"plan_digest"`
	CatalogRevision string          `json:"catalog_revision"`
	TargetID        string          `json:"target_id"`
	Complete        bool            `json:"complete"`
	StartedAt       time.Time       `json:"started_at"`
	FinishedAt      time.Time       `json:"finished_at"`
	Outcomes        []ActionOutcome `json:"outcomes"`
}

type ActionOutcome struct {
	ActionID         string `json:"action_id"`
	Status           string `json:"status"`
	RequestedVersion string `json:"requested_version"`
	InstalledVersion string `json:"installed_version,omitempty"`
	VerifiedVersion  string `json:"verified_version,omitempty"`
	Noop             bool   `json:"noop"`
	Reason           string `json:"reason,omitempty"`
}

func WriteReceipt(directory string, receipt Receipt) (string, error) {
	if err := ensureDirectory(directory); err != nil {
		return "", err
	}
	path := filepath.Join(directory, ReceiptFilename(receipt.PlanDigest))
	if err := ensureRegularOrMissing(path); err != nil {
		return "", err
	}
	receipt.SchemaVersion = "mds.receipt/v1"
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode receipt: %w", err)
	}
	encoded = append(encoded, '\n')

	temporary, err := os.CreateTemp(directory, ".receipt-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create receipt temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(0o600); err != nil {
		cleanup()
		return "", fmt.Errorf("restrict receipt temporary file: %w", err)
	}
	if _, err := temporary.Write(encoded); err != nil {
		cleanup()
		return "", fmt.Errorf("write receipt temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		cleanup()
		return "", fmt.Errorf("sync receipt temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return "", fmt.Errorf("close receipt temporary file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		_ = os.Remove(temporaryPath)
		return "", fmt.Errorf("publish receipt: %w", err)
	}
	return path, nil
}

func ReadReceipt(path string) (Receipt, error) {
	if err := ensureRegularOrMissing(path); err != nil {
		return Receipt{}, err
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Receipt{}, os.ErrNotExist
	}
	if err != nil {
		return Receipt{}, fmt.Errorf("read receipt: %w", err)
	}
	var receipt Receipt
	if err := json.Unmarshal(content, &receipt); err != nil {
		return Receipt{}, fmt.Errorf("decode receipt: %w", err)
	}
	return receipt, nil
}
