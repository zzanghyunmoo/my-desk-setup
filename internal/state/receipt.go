package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/durable"
)

const ReceiptSchema = "mds.receipt/v1"

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
	ActionID         string            `json:"action_id"`
	Status           string            `json:"status"`
	RequestedVersion string            `json:"requested_version"`
	InstalledVersion string            `json:"installed_version,omitempty"`
	VerifiedVersion  string            `json:"verified_version,omitempty"`
	Noop             bool              `json:"noop"`
	ReasonCode       string            `json:"reason_code,omitempty"`
	Reason           string            `json:"reason,omitempty"`
	Approval         map[string]string `json:"approval,omitempty"`
}

func WriteReceipt(directory string, receipt Receipt) (string, error) {
	if err := ensureDirectory(directory); err != nil {
		return "", err
	}
	completePath := filepath.Join(directory, ReceiptFilename(receipt.PlanDigest))
	partialPath := filepath.Join(
		directory,
		PartialReceiptFilename(receipt.PlanDigest),
	)
	path := partialPath
	if receipt.Complete {
		path = completePath
		if err := ensureRegularOrMissing(partialPath); err != nil {
			return "", err
		}
	}
	if err := ensureRegularOrMissing(path); err != nil {
		return "", err
	}
	receipt.SchemaVersion = ReceiptSchema
	encoded, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode receipt: %w", err)
	}
	encoded = append(encoded, '\n')

	if err := durable.WriteFile(path, encoded, 0o600); err != nil {
		return "", fmt.Errorf("publish receipt durably: %w", err)
	}
	if receipt.Complete {
		if err := durable.RemoveFile(partialPath); err != nil {
			return "", fmt.Errorf("clear partial receipt durably: %w", err)
		}
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
