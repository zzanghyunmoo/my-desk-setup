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
		Provenance: candidate.Provenance, Artifacts: candidate.Artifacts,
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
	plan := Plan{
		SchemaVersion: PlanSchema, ComponentID: candidate.ComponentID,
		LockKey:               component.VersionPolicy.LockKey,
		BeforeCatalogRevision: beforeRevision,
		AfterCatalogRevision:  afterRevision,
		Old:                   old, New: replacement, TargetPlan: targetPlan,
	}
	plan.Digest, err = Digest(plan)
	if err != nil {
		return Plan{}, catalog.Environment{}, err
	}
	return plan, updated, nil
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
