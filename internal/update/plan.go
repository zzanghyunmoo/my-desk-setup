package update

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"strings"

	exactartifact "github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
)

func Build(
	environment catalog.Environment,
	facts target.Facts,
	candidate Candidate,
) (Plan, catalog.Environment, error) {
	if err := validateCandidate(candidate); err != nil {
		return Plan{}, catalog.Environment{}, err
	}
	component, err := pinnedComponent(environment, candidate.ComponentID)
	if err != nil {
		return Plan{}, catalog.Environment{}, err
	}
	old := environment.Lock.Versions[component.VersionPolicy.LockKey]
	replacement := catalog.LockEntry{
		Version: candidate.Version, Source: candidate.Source,
		Provenance: candidate.Provenance, NPM: candidate.NPM,
		Artifacts: candidate.Artifacts,
	}
	if reflect.DeepEqual(old, replacement) {
		return Plan{}, catalog.Environment{}, errors.New("candidate does not change the committed lock")
	}
	if len(old.Artifacts) > 0 && len(replacement.Artifacts) == 0 {
		return Plan{}, catalog.Environment{}, errors.New(
			"vendor-managed candidate requires reviewed artifacts",
		)
	}
	beforeRevision, err := catalog.Revision(environment)
	if err != nil {
		return Plan{}, catalog.Environment{}, fmt.Errorf("before catalog revision: %w", err)
	}
	updated, err := cloneEnvironment(environment)
	if err != nil {
		return Plan{}, catalog.Environment{}, err
	}
	updated.Lock.Versions[component.VersionPolicy.LockKey] = replacement
	if err := catalog.Validate(updated); err != nil {
		return Plan{}, catalog.Environment{}, fmt.Errorf("candidate catalog validation: %w", err)
	}
	afterRevision, err := catalog.Revision(updated)
	if err != nil {
		return Plan{}, catalog.Environment{}, fmt.Errorf("after catalog revision: %w", err)
	}
	facts.CatalogRevision = afterRevision
	selection, err := planning.Components([]string{candidate.ComponentID})
	if err != nil {
		return Plan{}, catalog.Environment{}, err
	}
	targetPlan, err := planning.Build(updated, facts, selection)
	if err != nil {
		return Plan{}, catalog.Environment{}, fmt.Errorf("build resulting target plan: %w", err)
	}
	matrix, err := buildCompatibilityMatrix(
		updated,
		component,
		facts.CLIRevision,
		afterRevision,
	)
	if err != nil {
		return Plan{}, catalog.Environment{}, err
	}
	plan := Plan{
		SchemaVersion: PlanSchema, ComponentID: candidate.ComponentID,
		LockKey:               component.VersionPolicy.LockKey,
		BeforeCatalogRevision: beforeRevision,
		AfterCatalogRevision:  afterRevision,
		Old:                   old, New: replacement, TargetPlan: targetPlan,
		CompatibilityMatrix: matrix,
	}
	plan.Digest, err = Digest(plan)
	if err != nil {
		return Plan{}, catalog.Environment{}, err
	}
	return plan, updated, nil
}

func buildCompatibilityMatrix(
	environment catalog.Environment,
	component catalog.Component,
	cliRevision,
	catalogRevision string,
) ([]MatrixEntry, error) {
	selection, err := planning.Components([]string{component.ID})
	if err != nil {
		return nil, err
	}
	var matrix []MatrixEntry
	for _, targetKind := range catalog.TargetKinds {
		support := component.Targets[targetKind]
		if support.Status == catalog.StatusUnsupported {
			continue
		}
		for _, architecture := range []string{"amd64", "arm64"} {
			facts, err := compatibilityFacts(
				targetKind,
				architecture,
				cliRevision,
				catalogRevision,
			)
			if err != nil {
				return nil, err
			}
			artifactKey := ""
			if support.Installer == "vendor" {
				artifactKey = facts.OS + "-" + architecture
				lock := environment.Lock.Versions[component.VersionPolicy.LockKey]
				if _, exists := lock.Artifacts[artifactKey]; !exists {
					return nil, fmt.Errorf(
						"compatibility matrix %s/%s requires reviewed artifact %q",
						targetKind,
						architecture,
						artifactKey,
					)
				}
			}
			targetPlan, err := planning.Build(environment, facts, selection)
			if err != nil {
				return nil, fmt.Errorf(
					"compatibility matrix %s/%s: %w",
					targetKind,
					architecture,
					err,
				)
			}
			matrix = append(matrix, MatrixEntry{
				TargetKind: targetKind, TargetID: facts.ID.String(),
				Architecture: architecture, PlanDigest: targetPlan.Digest,
				ArtifactKey: artifactKey,
			})
		}
	}
	if len(matrix) == 0 {
		return nil, fmt.Errorf(
			"component %q has no supported compatibility target",
			component.ID,
		)
	}
	return matrix, nil
}

func compatibilityFacts(
	targetKind catalog.TargetKind,
	architecture,
	cliRevision,
	catalogRevision string,
) (target.Facts, error) {
	var (
		kind      target.Kind
		name      string
		osName    string
		osVersion string
		systemd   bool
	)
	switch targetKind {
	case catalog.TargetMacOSHost:
		kind, name, osName = target.KindMacOSHost, "local", "darwin"
	case catalog.TargetWindowsHost:
		kind, name, osName = target.KindWindowsHost, "local", "windows"
	case catalog.TargetWSLGuest:
		kind, name, osName = target.KindWSLGuest, "Ubuntu-26.04", "linux"
		osVersion, systemd = "26.04", true
	case catalog.TargetLimaGuest:
		kind, name, osName = target.KindLimaGuest, "mds", "linux"
		osVersion, systemd = "26.04", true
	default:
		return target.Facts{}, fmt.Errorf(
			"unsupported compatibility target %q",
			targetKind,
		)
	}
	id, err := target.NewID(kind, name)
	if err != nil {
		return target.Facts{}, err
	}
	return target.Facts{
		ID: id, OS: osName, OSVersion: osVersion,
		Architecture: architecture, Reachable: true,
		SystemdSupported: systemd, SystemdActive: systemd,
		CLIRevision: cliRevision, CatalogRevision: catalogRevision,
	}, nil
}

func validateCandidate(candidate Candidate) error {
	fields := map[string]string{
		"component_id": candidate.ComponentID,
		"version":      candidate.Version,
		"source":       candidate.Source,
		"provenance":   candidate.Provenance,
	}
	for name, value := range fields {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("candidate requires non-empty %s", name)
		}
		if value != strings.TrimSpace(value) {
			return fmt.Errorf("candidate %s cannot contain surrounding whitespace", name)
		}
	}
	if err := requireHTTPSURL(candidate.Provenance); err != nil {
		return fmt.Errorf("candidate provenance: %w", err)
	}
	if candidate.NPM != nil {
		if err := requireHTTPSURL(candidate.NPM.Tarball); err != nil {
			return fmt.Errorf("candidate npm tarball: %w", err)
		}
		if _, err := exactartifact.DecodeSHA512SRI(candidate.NPM.Integrity); err != nil {
			return fmt.Errorf("candidate npm SRI: %w", err)
		}
		if err := exactartifact.ValidateSHA256(candidate.NPM.SHA256); err != nil {
			return fmt.Errorf("candidate npm digest: %w", err)
		}
	}
	for platform, artifact := range candidate.Artifacts {
		if err := requireHTTPSURL(artifact.URL); err != nil {
			return fmt.Errorf("candidate artifact %q URL: %w", platform, err)
		}
		checksum, err := hex.DecodeString(artifact.SHA256)
		if err != nil || len(checksum) != sha256.Size {
			return fmt.Errorf(
				"candidate artifact %q requires a 64-character hexadecimal SHA-256",
				platform,
			)
		}
	}
	return nil
}

func requireHTTPSURL(value string) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("must be an absolute HTTPS URL")
	}
	return nil
}

func Digest(plan Plan) (string, error) {
	plan.Digest = ""
	encoded, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("encode update plan: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func Verify(plan Plan, expected string) error {
	if plan.SchemaVersion != PlanSchema {
		return fmt.Errorf("unsupported update plan schema %q", plan.SchemaVersion)
	}
	actual, err := Digest(plan)
	if err != nil {
		return err
	}
	if plan.Digest != actual {
		return fmt.Errorf("update plan payload digest mismatch: embedded=%s actual=%s", plan.Digest, actual)
	}
	if expected != actual {
		return fmt.Errorf("update plan digest mismatch: expected=%s actual=%s", expected, actual)
	}
	return nil
}

func DecodeCandidate(content []byte) (Candidate, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var candidate Candidate
	if err := decoder.Decode(&candidate); err != nil {
		return Candidate{}, fmt.Errorf("decode update candidate: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Candidate{}, errors.New("update candidate contains trailing JSON")
		}
		return Candidate{}, fmt.Errorf("decode update candidate trailer: %w", err)
	}
	return candidate, nil
}

func pinnedComponent(
	environment catalog.Environment,
	componentID string,
) (catalog.Component, error) {
	for _, component := range environment.Catalog.Components {
		if component.ID != componentID {
			continue
		}
		if component.VersionPolicy.Mode != "pinned" {
			return catalog.Component{}, fmt.Errorf(
				"component %q is %s-owned and has no exact lock update",
				componentID,
				component.VersionPolicy.Mode,
			)
		}
		return component, nil
	}
	return catalog.Component{}, fmt.Errorf("unknown component %q", componentID)
}

func cloneEnvironment(environment catalog.Environment) (catalog.Environment, error) {
	encoded, err := json.Marshal(environment)
	if err != nil {
		return catalog.Environment{}, fmt.Errorf("encode environment clone: %w", err)
	}
	var clone catalog.Environment
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return catalog.Environment{}, fmt.Errorf("decode environment clone: %w", err)
	}
	return clone, nil
}
