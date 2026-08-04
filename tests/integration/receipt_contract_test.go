package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/output"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/state"
	updateflow "github.com/zzanghyunmoo/my-desk-setup/internal/update"
)

const receiptSchema = "mds.receipt/v1"

func TestRunnerReceiptSchemaSurvivesDirectAndNestedJSON(t *testing.T) {
	plan := singleActionPlan(t, "lima-guest:mds")
	receipt, err := testRunner(newFakeAdapter()).Apply(
		context.Background(),
		plan,
		plan.Digest,
		filepath.Join(t.TempDir(), "state"),
	)
	if err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	if receipt.SchemaVersion != receiptSchema {
		t.Errorf(
			"returned receipt schema = %q, want %q",
			receipt.SchemaVersion,
			receiptSchema,
		)
	}

	t.Run("direct apply JSON", func(t *testing.T) {
		var encoded bytes.Buffer
		if err := output.JSON(&encoded, receipt); err != nil {
			t.Fatalf("JSON(receipt): %v", err)
		}
		var document struct {
			SchemaVersion string `json:"schema_version"`
		}
		if err := json.Unmarshal(encoded.Bytes(), &document); err != nil {
			t.Fatalf("decode direct receipt JSON: %v", err)
		}
		if document.SchemaVersion != receiptSchema {
			t.Fatalf(
				"direct receipt schema = %q, want %q",
				document.SchemaVersion,
				receiptSchema,
			)
		}
	})

	t.Run("nested update JSON", func(t *testing.T) {
		result := updateflow.Result{
			SchemaVersion: updateflow.ResultSchema,
			UpdateDigest:  "sha256:update",
			Receipt:       receipt,
		}
		var encoded bytes.Buffer
		if err := output.JSON(&encoded, result); err != nil {
			t.Fatalf("JSON(update result): %v", err)
		}
		var document struct {
			Receipt struct {
				SchemaVersion string `json:"schema_version"`
			} `json:"receipt"`
		}
		if err := json.Unmarshal(encoded.Bytes(), &document); err != nil {
			t.Fatalf("decode nested update JSON: %v", err)
		}
		if document.Receipt.SchemaVersion != receiptSchema {
			t.Fatalf(
				"nested receipt schema = %q, want %q",
				document.Receipt.SchemaVersion,
				receiptSchema,
			)
		}
	})
}

func TestHarnessReceiptPreservesSecretFreeApprovalIdentity(t *testing.T) {
	plan := singleActionPlan(t, "macos-host:local")
	action := &plan.Actions[0]
	action.ComponentID = "oh-my-harness"
	action.ID = plan.Target.ID.String() + "/oh-my-harness"
	action.Inputs = map[string]string{
		"artifact_archive_sha256":          strings.Repeat("a", 64),
		"harness_child_digest":             strings.Repeat("b", 64),
		"harness_child_catalog_revision":   strings.Repeat("c", 64),
		"harness_config_digest":            "sha256:" + strings.Repeat("d", 64),
		"harness_addon_summary_digest":     "sha256:" + strings.Repeat("e", 64),
		"harness_ownership_summary_digest": "sha256:" + strings.Repeat("f", 64),
	}
	plan.Selection = []string{"oh-my-harness"}
	var err error
	plan.Digest, err = planning.Digest(plan)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := testRunner(newFakeAdapter()).Apply(
		context.Background(), plan, plan.Digest, filepath.Join(t.TempDir(), "state"),
	)
	if err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	if len(receipt.Outcomes) != 1 ||
		!reflect.DeepEqual(receipt.Outcomes[0].Approval, action.Inputs) {
		t.Fatalf("harness receipt approval = %+v, want %+v", receipt.Outcomes, action.Inputs)
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"token", "password", "/Users/", "C:\\Users\\"} {
		if strings.Contains(strings.ToLower(string(encoded)), strings.ToLower(forbidden)) {
			t.Fatalf("receipt leaked forbidden material %q: %s", forbidden, encoded)
		}
	}
}

func TestCompleteReceiptSurvivesFailedRetryUntilLaterSuccess(t *testing.T) {
	plan := singleActionPlan(t, "lima-guest:mds")
	adapter := newFakeAdapter()
	runner := testRunner(adapter)
	stateRoot := filepath.Join(t.TempDir(), "state")

	first, err := runner.Apply(context.Background(), plan, plan.Digest, stateRoot)
	if err != nil {
		t.Fatalf("first Apply(): %v", err)
	}
	if !first.Complete {
		t.Fatalf("first receipt = %+v, want complete", first)
	}

	adapter.mutex.Lock()
	adapter.installed["a"] = false
	adapter.failOnce["a"] = true
	adapter.mutex.Unlock()

	failed, err := runner.Apply(context.Background(), plan, plan.Digest, stateRoot)
	if err != nil {
		t.Fatalf("failed Apply(): %v", err)
	}
	if failed.Complete {
		t.Fatalf("failed receipt = %+v, want incomplete", failed)
	}

	paths, err := state.NewPaths(stateRoot, plan.Target.ID.String())
	if err != nil {
		t.Fatalf("NewPaths(): %v", err)
	}
	completePath := filepath.Join(
		paths.Receipts,
		state.ReceiptFilename(plan.Digest),
	)
	partialPath := filepath.Join(
		paths.Receipts,
		strings.TrimSuffix(state.ReceiptFilename(plan.Digest), ".json")+
			".partial.json",
	)
	preserved, err := state.ReadReceipt(completePath)
	if err != nil {
		t.Fatalf("ReadReceipt(complete): %v", err)
	}
	if !preserved.Complete ||
		!preserved.FinishedAt.Equal(first.FinishedAt) ||
		!reflect.DeepEqual(preserved.Outcomes, first.Outcomes) {
		t.Fatalf(
			"complete receipt was replaced: got=%+v first=%+v",
			preserved,
			first,
		)
	}
	currentPartial, err := state.ReadReceipt(partialPath)
	if err != nil {
		t.Fatalf("ReadReceipt(partial): %v", err)
	}
	if currentPartial.Complete ||
		!currentPartial.FinishedAt.Equal(failed.FinishedAt) ||
		!reflect.DeepEqual(currentPartial.Outcomes, failed.Outcomes) {
		t.Fatalf(
			"partial receipt = %+v, want current failure %+v",
			currentPartial,
			failed,
		)
	}

	recovered, err := runner.Apply(
		context.Background(),
		plan,
		plan.Digest,
		stateRoot,
	)
	if err != nil {
		t.Fatalf("recovery Apply(): %v", err)
	}
	if !recovered.Complete {
		t.Fatalf("recovery receipt = %+v, want complete", recovered)
	}
	published, err := state.ReadReceipt(completePath)
	if err != nil {
		t.Fatalf("ReadReceipt(recovered complete): %v", err)
	}
	if !published.FinishedAt.Equal(recovered.FinishedAt) ||
		!reflect.DeepEqual(published.Outcomes, recovered.Outcomes) {
		t.Fatalf(
			"published complete receipt = %+v, want recovered %+v",
			published,
			recovered,
		)
	}
	if _, err := state.ReadReceipt(partialPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial receipt remains after success: %v", err)
	}
}
