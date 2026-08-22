package capability

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestMatchesExpectedRejectsTruncatedIDEReceipt(t *testing.T) {
	receipt := Aggregate(
		[]string{"lsp.java"},
		[]CapabilityCheck{NewCheck(
			"lsp.java", KindLSP, "nvim-jvm", StatusPass, "ready", "",
		)},
	)
	if MatchesExpected([]string{"nvim-jvm"}, &receipt) {
		t.Fatal("truncated JVM receipt matched the plan-derived capability contract")
	}
	if len(ExpectedIDs([]string{"nvim-jvm", "nvim-dotnet"})) == 0 {
		t.Fatal("combined IDE capability contract is empty")
	}
}

func TestAggregateAcceptsExactPassingCapabilitySet(t *testing.T) {
	receipt := Aggregate([]string{"artifact.jdtls", "dap.java"}, []CapabilityCheck{
		NewCheck("artifact.jdtls", KindArtifact, "jvm", StatusPass, "ready", "installed exact tree"),
		NewDAPCheck("dap.java", "jvm", StatusPass, "ready", DAPOutcome{
			BreakpointVerified:   true,
			StoppedAtSource:      true,
			StoppedSourceID:      "fixture.main",
			StoppedLine:          12,
			StackObserved:        true,
			ScopesObserved:       true,
			KnownVariablePresent: true,
			Continued:            true,
			SteppedIn:            true,
			SteppedOver:          true,
			Terminated:           true,
		}),
	})
	if !receipt.Ready || len(receipt.Problems) != 0 {
		t.Fatalf("receipt = %#v, want ready", receipt)
	}
}

func TestAggregateRejectsMachineLocalDAPSourceAttribution(t *testing.T) {
	receipt := Aggregate([]string{"dap.java"}, []CapabilityCheck{
		NewDAPCheck("dap.java", "jvm", StatusPass, "ready", DAPOutcome{
			BreakpointVerified: true, StoppedAtSource: true,
			StoppedSourceID: "/Users/alice/project/Main.java", StoppedLine: 12,
			StackObserved: true, ScopesObserved: true, KnownVariablePresent: true,
			Continued: true, SteppedIn: true, SteppedOver: true, Terminated: true,
		}),
	})
	if receipt.Ready || !containsProblem(receipt.Problems, "dap-incomplete") {
		t.Fatalf("receipt = %#v, want machine-local source attribution rejection", receipt)
	}
}

func TestAggregateFailsClosedForSetAndStatusFailures(t *testing.T) {
	tests := []struct {
		name   string
		checks []CapabilityCheck
		want   string
	}{
		{name: "missing", checks: nil, want: "missing"},
		{name: "duplicate", checks: []CapabilityCheck{
			NewCheck("action.build", KindProjectAction, "jvm", StatusPass, "ready", ""),
			NewCheck("action.build", KindProjectAction, "jvm", StatusPass, "ready", ""),
		}, want: "duplicate"},
		{name: "unknown", checks: []CapabilityCheck{
			NewCheck("action.unknown", KindProjectAction, "jvm", StatusPass, "ready", ""),
		}, want: "unknown"},
		{name: "failed", checks: []CapabilityCheck{
			NewCheck("action.build", KindProjectAction, "jvm", StatusFailed, "exit-nonzero", "failed"),
		}, want: "non-pass"},
		{name: "timeout", checks: []CapabilityCheck{
			NewCheck("action.build", KindProjectAction, "jvm", StatusTimeout, "timeout", "timed out"),
		}, want: "non-pass"},
		{name: "blocked", checks: []CapabilityCheck{
			NewCheck("action.build", KindProjectAction, "jvm", StatusBlocked, "workspace-untrusted", "blocked"),
		}, want: "non-pass"},
		{name: "not-run", checks: []CapabilityCheck{
			NewCheck("action.build", KindProjectAction, "jvm", StatusNotRun, "not-run", "not run"),
		}, want: "non-pass"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt := Aggregate([]string{"action.build"}, test.checks)
			if receipt.Ready || !containsProblem(receipt.Problems, test.want) {
				t.Fatalf("receipt = %#v, want non-ready problem %q", receipt, test.want)
			}
		})
	}
}

func TestAggregateRejectsHandshakeOnlyDAPPass(t *testing.T) {
	receipt := Aggregate([]string{"dap.kotlin"}, []CapabilityCheck{
		NewDAPCheck("dap.kotlin", "jvm", StatusPass, "ready", DAPOutcome{
			BreakpointVerified: true,
		}),
	})
	if receipt.Ready || !containsProblem(receipt.Problems, "dap-incomplete") {
		t.Fatalf("receipt = %#v, want incomplete DAP failure", receipt)
	}
}

func TestReceiptJSONIsBoundedAndSecretFree(t *testing.T) {
	secret := "ghp_canary123"
	receipt := Aggregate([]string{"action.run"}, []CapabilityCheck{
		NewCheck("action.run", KindProjectAction, "dotnet", StatusFailed, "exit-nonzero", "token="+secret+" "+strings.Repeat("failure ", 1000)),
	})
	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || len(encoded) > MaxReceiptBytes {
		t.Fatalf("receipt leaked secret or exceeded bound (%d bytes): %s", len(encoded), encoded)
	}
	for _, forbidden := range []string{"environment", "source_content", "variable_value", "stdout", "stderr"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("receipt contains forbidden raw field %q: %s", forbidden, encoded)
		}
	}
}

func TestDirectReceiptMarshallingDropsInvalidSecretBearingIdentifiers(t *testing.T) {
	secret := "ghp_canary123"
	receipt := Receipt{
		SchemaVersion: SchemaVersion,
		ExpectedIDs:   []string{secret},
		Checks: []CapabilityCheck{{
			ID: secret, Kind: KindProjectAction, ComponentID: secret,
			Status: StatusFailed, ReasonCode: secret, Attribution: "token=" + secret,
			DAP: &DAPOutcome{StoppedSourceID: secret},
		}},
	}
	encoded, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("direct receipt leaked secret-bearing identifier: %s", encoded)
	}
	if receipt.Checks[0].DAP.StoppedSourceID != secret {
		t.Fatal("receipt marshalling mutated the caller-owned DAP outcome")
	}
}

func containsProblem(problems []Problem, code string) bool {
	return slices.ContainsFunc(problems, func(problem Problem) bool {
		return problem.Code == code
	})
}
