package state

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type JournalEvent struct {
	SchemaVersion string    `json:"schema_version"`
	At            time.Time `json:"at"`
	PlanDigest    string    `json:"plan_digest"`
	ActionID      string    `json:"action_id"`
	Phase         string    `json:"phase"`
	Detail        string    `json:"detail,omitempty"`
}

type Journal struct {
	path string
}

func NewJournal(path string) Journal {
	return Journal{path: path}
}

func (journal Journal) Append(event JournalEvent) error {
	if err := ensureRegularOrMissing(journal.path); err != nil {
		return err
	}
	event.SchemaVersion = "mds.journal/v1"
	encoded, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode journal event: %w", err)
	}
	file, err := os.OpenFile(
		journal.path,
		os.O_WRONLY|os.O_APPEND|os.O_CREATE,
		0o600,
	)
	if err != nil {
		return fmt.Errorf("open journal: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("append journal: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync journal: %w", err)
	}
	return nil
}
