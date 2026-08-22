package capability

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

const (
	SchemaVersion        = "mds.capability-checks/v1"
	MaxCapabilityChecks  = 128
	MaxAttributionLength = 512
	MaxReceiptBytes      = 256 << 10
)

type Kind string

const (
	KindArtifact      Kind = "artifact"
	KindConfiguration Kind = "configuration"
	KindLSP           Kind = "lsp"
	KindMixedDocument Kind = "mixed-document"
	KindProjectAction Kind = "project-action"
	KindDAP           Kind = "dap"
	KindActualTarget  Kind = "actual-target"
)

type Status string

const (
	StatusPass    Status = "pass"
	StatusFailed  Status = "failed"
	StatusTimeout Status = "timeout"
	StatusBlocked Status = "blocked"
	StatusNotRun  Status = "not-run"
)

type CapabilityCheck struct {
	ID          string      `json:"id"`
	Kind        Kind        `json:"kind"`
	ComponentID string      `json:"component_id"`
	Status      Status      `json:"status"`
	ReasonCode  string      `json:"reason_code"`
	Attribution string      `json:"attribution,omitempty"`
	DAP         *DAPOutcome `json:"dap,omitempty"`
}

// DAPOutcome records only structural proof. Variable values and raw protocol
// messages are intentionally not representable in a durable receipt.
type DAPOutcome struct {
	BreakpointVerified   bool   `json:"breakpoint_verified"`
	StoppedAtSource      bool   `json:"stopped_at_source"`
	StoppedSourceID      string `json:"stopped_source_id"`
	StoppedLine          int    `json:"stopped_line"`
	StackObserved        bool   `json:"stack_observed"`
	ScopesObserved       bool   `json:"scopes_observed"`
	KnownVariablePresent bool   `json:"known_variable_present"`
	Continued            bool   `json:"continued"`
	SteppedIn            bool   `json:"stepped_in"`
	SteppedOver          bool   `json:"stepped_over"`
	Terminated           bool   `json:"terminated"`
}

type Problem struct {
	Code         string `json:"code"`
	CapabilityID string `json:"capability_id,omitempty"`
	Detail       string `json:"detail,omitempty"`
}

type Receipt struct {
	SchemaVersion string            `json:"schema_version"`
	Ready         bool              `json:"ready"`
	ExpectedIDs   []string          `json:"expected_ids"`
	Checks        []CapabilityCheck `json:"checks"`
	Problems      []Problem         `json:"problems,omitempty"`
}

var identifierPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9._-]{0,126}[a-z0-9])?$`)

func NewCheck(
	id string,
	kind Kind,
	componentID string,
	status Status,
	reasonCode,
	attribution string,
) CapabilityCheck {
	return CapabilityCheck{
		ID: id, Kind: kind, ComponentID: componentID, Status: status,
		ReasonCode: reasonCode, Attribution: sanitize(attribution),
	}
}

func NewDAPCheck(
	id,
	componentID string,
	status Status,
	reasonCode string,
	outcome DAPOutcome,
) CapabilityCheck {
	check := NewCheck(id, KindDAP, componentID, status, reasonCode, "")
	check.DAP = &outcome
	return check
}

func Aggregate(expected []string, checks []CapabilityCheck) Receipt {
	receipt := Receipt{
		SchemaVersion: SchemaVersion,
		ExpectedIDs:   append([]string(nil), expected...),
		Checks:        append([]CapabilityCheck(nil), checks...),
	}
	for index := range receipt.Checks {
		if receipt.Checks[index].DAP != nil {
			outcome := *receipt.Checks[index].DAP
			receipt.Checks[index].DAP = &outcome
		}
	}
	if len(receipt.ExpectedIDs) > MaxCapabilityChecks {
		receipt.Problems = append(receipt.Problems, Problem{
			Code: "expected-set-too-large",
		})
		receipt.ExpectedIDs = receipt.ExpectedIDs[:MaxCapabilityChecks]
	}
	if len(receipt.Checks) > MaxCapabilityChecks {
		receipt.Problems = append(receipt.Problems, Problem{
			Code: "check-set-too-large",
		})
		receipt.Checks = receipt.Checks[:MaxCapabilityChecks]
	}
	sort.Strings(receipt.ExpectedIDs)
	expectedSet := make(map[string]struct{}, len(receipt.ExpectedIDs))
	for _, id := range receipt.ExpectedIDs {
		if !validIdentifier(id) {
			receipt.Problems = append(receipt.Problems, Problem{
				Code: "invalid-expected-id", CapabilityID: sanitize(id),
			})
			continue
		}
		if _, duplicate := expectedSet[id]; duplicate {
			receipt.Problems = append(receipt.Problems, Problem{
				Code: "duplicate-expected", CapabilityID: id,
			})
		}
		expectedSet[id] = struct{}{}
	}

	seen := make(map[string]int, len(receipt.Checks))
	for index := range receipt.Checks {
		check := &receipt.Checks[index]
		check.Attribution = sanitize(check.Attribution)
		seen[check.ID]++
		if !validIdentifier(check.ID) || !validIdentifier(check.ComponentID) ||
			!validIdentifier(check.ReasonCode) || !validKind(check.Kind) ||
			!validStatus(check.Status) {
			receipt.Problems = append(receipt.Problems, Problem{
				Code: "invalid-check", CapabilityID: sanitize(check.ID),
			})
			continue
		}
		if _, known := expectedSet[check.ID]; !known {
			receipt.Problems = append(receipt.Problems, Problem{
				Code: "unknown", CapabilityID: check.ID,
			})
		}
		if seen[check.ID] > 1 {
			receipt.Problems = append(receipt.Problems, Problem{
				Code: "duplicate", CapabilityID: check.ID,
			})
		}
		if check.Status != StatusPass {
			receipt.Problems = append(receipt.Problems, Problem{
				Code: "non-pass", CapabilityID: check.ID,
				Detail: string(check.Status),
			})
		}
		if check.Kind == KindDAP && check.Status == StatusPass &&
			(check.DAP == nil || !check.DAP.complete()) {
			receipt.Problems = append(receipt.Problems, Problem{
				Code: "dap-incomplete", CapabilityID: check.ID,
			})
		}
		if check.Kind != KindDAP && check.DAP != nil {
			receipt.Problems = append(receipt.Problems, Problem{
				Code: "unexpected-dap-outcome", CapabilityID: check.ID,
			})
		}
	}
	for _, id := range receipt.ExpectedIDs {
		if seen[id] == 0 {
			receipt.Problems = append(receipt.Problems, Problem{
				Code: "missing", CapabilityID: id,
			})
		}
	}
	sort.SliceStable(receipt.Checks, func(left, right int) bool {
		return receipt.Checks[left].ID < receipt.Checks[right].ID
	})
	sort.SliceStable(receipt.Problems, func(left, right int) bool {
		if receipt.Problems[left].CapabilityID != receipt.Problems[right].CapabilityID {
			return receipt.Problems[left].CapabilityID < receipt.Problems[right].CapabilityID
		}
		return receipt.Problems[left].Code < receipt.Problems[right].Code
	})
	receipt.Ready = len(receipt.Problems) == 0
	return receipt
}

func (receipt Receipt) MarshalJSON() ([]byte, error) {
	type wireReceipt Receipt
	copy := Aggregate(receipt.ExpectedIDs, receipt.Checks)
	if receipt.SchemaVersion != SchemaVersion {
		copy.Ready = false
		copy.Problems = append(copy.Problems, Problem{Code: "schema-version"})
	}
	for index := range copy.ExpectedIDs {
		copy.ExpectedIDs[index] = wireIdentifier(copy.ExpectedIDs[index])
	}
	for index := range copy.Checks {
		copy.Checks[index].ID = wireIdentifier(copy.Checks[index].ID)
		copy.Checks[index].ComponentID = wireIdentifier(copy.Checks[index].ComponentID)
		copy.Checks[index].ReasonCode = wireIdentifier(copy.Checks[index].ReasonCode)
		copy.Checks[index].Attribution = sanitize(copy.Checks[index].Attribution)
		if !validKind(copy.Checks[index].Kind) {
			copy.Checks[index].Kind = "invalid"
		}
		if !validStatus(copy.Checks[index].Status) {
			copy.Checks[index].Status = "invalid"
		}
		if copy.Checks[index].DAP != nil {
			copy.Checks[index].DAP.StoppedSourceID = wireIdentifier(
				copy.Checks[index].DAP.StoppedSourceID,
			)
		}
	}
	for index := range copy.Problems {
		copy.Problems[index].Code = wireIdentifier(copy.Problems[index].Code)
		copy.Problems[index].CapabilityID = wireIdentifier(copy.Problems[index].CapabilityID)
		copy.Problems[index].Detail = sanitize(copy.Problems[index].Detail)
	}
	data, err := json.Marshal(wireReceipt(copy))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxReceiptBytes {
		return nil, fmt.Errorf("capability receipt exceeds %d bytes", MaxReceiptBytes)
	}
	return data, nil
}

func wireIdentifier(value string) string {
	if value == "" {
		return ""
	}
	if !validIdentifier(value) || sanitize(value) != value {
		return "invalid"
	}
	return value
}

func (outcome DAPOutcome) complete() bool {
	return outcome.BreakpointVerified && outcome.StoppedAtSource &&
		validIdentifier(outcome.StoppedSourceID) &&
		outcome.StoppedLine > 0 && outcome.StoppedLine <= 10_000_000 &&
		outcome.StackObserved && outcome.ScopesObserved &&
		outcome.KnownVariablePresent && outcome.Continued &&
		outcome.SteppedIn && outcome.SteppedOver && outcome.Terminated
}

func validIdentifier(value string) bool {
	return identifierPattern.MatchString(value)
}

func validKind(kind Kind) bool {
	switch kind {
	case KindArtifact, KindConfiguration, KindLSP, KindMixedDocument,
		KindProjectAction, KindDAP, KindActualTarget:
		return true
	default:
		return false
	}
}

func validStatus(status Status) bool {
	switch status {
	case StatusPass, StatusFailed, StatusTimeout, StatusBlocked, StatusNotRun:
		return true
	default:
		return false
	}
}

func sanitize(value string) string {
	value = transport.SanitizeDiagnostic(strings.TrimSpace(value))
	if len(value) > MaxAttributionLength {
		return value[:MaxAttributionLength] + " [attribution truncated]"
	}
	return value
}
