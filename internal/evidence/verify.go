package evidence

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	exactartifact "github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
	"github.com/zzanghyunmoo/my-desk-setup/internal/doctor"
	"github.com/zzanghyunmoo/my-desk-setup/internal/durable"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
)

const maxEvidenceFileSize = 4 << 20

var (
	checksummedFiles = []string{
		DoctorFile,
		ManifestFile,
		PlanFile,
	}
	expectedFiles = append([]string{ChecksumsFile}, checksummedFiles...)
)

func Verify(root string, options VerifyOptions) (Manifest, error) {
	files, err := readBundleFiles(root)
	if err != nil {
		return Manifest{}, err
	}
	if err := verifyChecksums(files); err != nil {
		return Manifest{}, err
	}
	for _, name := range checksummedFiles {
		if err := scanEvidenceMaterial(name, files[name]); err != nil {
			return Manifest{}, err
		}
	}

	var manifest Manifest
	if err := decodeStrict(files[ManifestFile], &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode %s: %w", ManifestFile, err)
	}
	var plan planning.Plan
	if err := decodeStrict(files[PlanFile], &plan); err != nil {
		return Manifest{}, fmt.Errorf("decode %s: %w", PlanFile, err)
	}
	var snapshot DoctorSnapshot
	if err := decodeStrict(files[DoctorFile], &snapshot); err != nil {
		return Manifest{}, fmt.Errorf("decode %s: %w", DoctorFile, err)
	}

	if err := validateManifest(manifest, options); err != nil {
		return Manifest{}, err
	}
	cli, err := parseCLIIdentity(manifest.CLI)
	if err != nil {
		return Manifest{}, err
	}
	components, err := validatePlanDoctor(plan, snapshot, cli, manifest.Target.ID)
	if err != nil {
		return Manifest{}, err
	}
	if !reflect.DeepEqual(manifest.Components, components) {
		return Manifest{}, errors.New(
			"manifest component outcomes do not match bounded doctor verification",
		)
	}
	identity, err := targetIdentity(plan.Target)
	if err != nil {
		return Manifest{}, err
	}
	if manifest.Target != identity {
		return Manifest{}, errors.New("manifest target identity does not match plan")
	}
	if manifest.CatalogRevision != plan.CatalogRevision {
		return Manifest{}, errors.New("manifest catalog revision does not match plan")
	}
	if manifest.PlanDigest != plan.Digest {
		return Manifest{}, errors.New("manifest plan digest does not match plan")
	}
	if manifest.ApplyReceipt == nil {
		return Manifest{}, errors.New("manifest is missing reviewed apply receipt")
	}
	if err := validateCertificationReceipt(
		*manifest.ApplyReceipt,
		plan,
		!manifest.ApplyReceipt.Complete,
		false,
	); err != nil {
		return Manifest{}, fmt.Errorf("validate manifest apply receipt: %w", err)
	}
	if manifest.ApplyReceipt.Complete {
		if manifest.RepeatReceipt == nil {
			return Manifest{}, errors.New(
				"complete certification is missing repeat apply receipt",
			)
		}
		if err := validateCertificationReceipt(
			*manifest.RepeatReceipt,
			plan,
			false,
			true,
		); err != nil {
			return Manifest{}, fmt.Errorf("validate manifest repeat receipt: %w", err)
		}
	} else if manifest.RepeatReceipt != nil {
		return Manifest{}, errors.New(
			"incomplete certification cannot contain repeat apply receipt",
		)
	}
	expectedStatus := StatusVerified
	if len(plan.Blockers) > 0 ||
		!manifest.ApplyReceipt.Complete ||
		manifest.RepeatReceipt == nil ||
		!snapshot.Ready ||
		!targetIdentityComplete(plan.Target) {
		expectedStatus = StatusBlocked
	}
	if manifest.Status != expectedStatus {
		return Manifest{}, fmt.Errorf(
			"evidence status %q does not match recomputed status %q",
			manifest.Status,
			expectedStatus,
		)
	}
	if options.RequirePublicationAcceptable {
		if err := validatePublicationAcceptable(manifest, plan, snapshot); err != nil {
			return Manifest{}, err
		}
	}
	return manifest, nil
}

func validateManifest(manifest Manifest, options VerifyOptions) error {
	if manifest.SchemaVersion != SchemaVersion {
		return fmt.Errorf(
			"evidence schema = %q, want %q",
			manifest.SchemaVersion,
			SchemaVersion,
		)
	}
	if manifest.CaptureKind != CaptureKindActualTarget {
		return fmt.Errorf(
			"capture kind = %q, want %q",
			manifest.CaptureKind,
			CaptureKindActualTarget,
		)
	}
	if manifest.Status != StatusBlocked && manifest.Status != StatusVerified {
		return fmt.Errorf(
			"actual target evidence cannot claim status %q",
			manifest.Status,
		)
	}
	if exactartifact.ValidateSHA256(manifest.BinarySHA256) != nil {
		return errors.New(
			"evidence binary_sha256 must be 64 lowercase hex characters",
		)
	}
	capturedAt, err := strconv.ParseInt(string(manifest.CapturedAtUnix), 10, 64)
	if err != nil {
		return fmt.Errorf("parse captured_at_unix: %w", err)
	}
	if capturedAt < 0 {
		return errors.New("captured_at_unix must not be negative")
	}
	if !options.Now.IsZero() {
		captured := time.Unix(capturedAt, 0)
		if captured.After(options.Now.Add(5 * time.Minute)) {
			return errors.New("target evidence timestamp is in the future")
		}
		if options.MaxAge > 0 && options.Now.Sub(captured) > options.MaxAge {
			return errors.New("target evidence timestamp is stale")
		}
	}
	if options.ExpectedCLIRevision != "" &&
		manifest.CLI.Revision != options.ExpectedCLIRevision {
		return fmt.Errorf(
			"stale CLI revision: evidence=%q expected=%q",
			manifest.CLI.Revision,
			options.ExpectedCLIRevision,
		)
	}
	if options.ExpectedCatalogRevision != "" &&
		manifest.CatalogRevision != options.ExpectedCatalogRevision {
		return fmt.Errorf(
			"stale catalog revision: evidence=%q expected=%q",
			manifest.CatalogRevision,
			options.ExpectedCatalogRevision,
		)
	}
	if options.ExpectedPlanDigest != "" &&
		manifest.PlanDigest != options.ExpectedPlanDigest {
		return fmt.Errorf(
			"stale plan digest: evidence=%q expected=%q",
			manifest.PlanDigest,
			options.ExpectedPlanDigest,
		)
	}
	if options.ExpectedTargetID != "" &&
		manifest.Target.ID != options.ExpectedTargetID {
		return fmt.Errorf(
			"wrong target evidence: evidence=%q expected=%q",
			manifest.Target.ID,
			options.ExpectedTargetID,
		)
	}
	if options.ExpectedBinarySHA256 != "" &&
		manifest.BinarySHA256 != options.ExpectedBinarySHA256 {
		return fmt.Errorf(
			"wrong release binary: evidence=%q expected=%q",
			manifest.BinarySHA256,
			options.ExpectedBinarySHA256,
		)
	}
	return nil
}

func validatePublicationAcceptable(
	manifest Manifest,
	plan planning.Plan,
	snapshot DoctorSnapshot,
) error {
	if manifest.Status == StatusVerified {
		return nil
	}
	if manifest.Status != StatusBlocked {
		return fmt.Errorf(
			"evidence status %q is not publication acceptable",
			manifest.Status,
		)
	}
	if !targetIdentityComplete(plan.Target) {
		return errors.New(
			"blocked evidence with incomplete target identity is not publication acceptable",
		)
	}
	if len(plan.Actions) != len(snapshot.Checks) {
		return errors.New(
			"blocked evidence does not cover every requested component",
		)
	}
	actionRequired := 0
	checks := make(map[string]ComponentCheck, len(snapshot.Checks))
	for _, check := range snapshot.Checks {
		checks[check.ActionID] = check
	}
	for _, action := range plan.Actions {
		check := checks[action.ID]
		if check.Status == "ready" {
			continue
		}
		if action.Status != planning.ActionActionRequired ||
			check.Status != "action-required" ||
			check.ReasonCode != "action-required" {
			return fmt.Errorf(
				"component %q outcome %q/%q is not publication acceptable",
				action.ComponentID,
				action.Status,
				check.Status,
			)
		}
		actionRequired++
	}
	if actionRequired == 0 {
		return errors.New(
			"blocked evidence has no honest action-required outcome",
		)
	}
	return nil
}

func validatePlanDoctor(
	plan planning.Plan,
	snapshot DoctorSnapshot,
	cli CLIIdentity,
	expectedTargetID string,
) ([]ComponentCheck, error) {
	if plan.SchemaVersion != planning.PlanSchema {
		return nil, fmt.Errorf(
			"plan schema = %q, want %q",
			plan.SchemaVersion,
			planning.PlanSchema,
		)
	}
	recomputedDigest, err := planning.Digest(plan)
	if err != nil {
		return nil, fmt.Errorf("recompute plan digest: %w", err)
	}
	if plan.Digest != recomputedDigest {
		return nil, fmt.Errorf(
			"plan digest mismatch: document=%q recomputed=%q",
			plan.Digest,
			recomputedDigest,
		)
	}
	if plan.Target.ID.String() != expectedTargetID {
		return nil, fmt.Errorf(
			"plan target = %q, want %q",
			plan.Target.ID.String(),
			expectedTargetID,
		)
	}
	if plan.Target.CLIRevision != cli.Revision {
		return nil, fmt.Errorf(
			"plan CLI revision = %q, want %q",
			plan.Target.CLIRevision,
			cli.Revision,
		)
	}
	if plan.Target.CatalogRevision != plan.CatalogRevision {
		return nil, errors.New(
			"plan target catalog revision does not match plan catalog revision",
		)
	}
	if err := validatePlanBlockers(plan, expectedTargetID); err != nil {
		return nil, err
	}
	if snapshot.SchemaVersion != doctor.SchemaVersion {
		return nil, fmt.Errorf(
			"doctor schema = %q, want %q",
			snapshot.SchemaVersion,
			doctor.SchemaVersion,
		)
	}
	if snapshot.CatalogRevision != plan.CatalogRevision {
		return nil, errors.New("doctor catalog revision does not match plan")
	}
	if snapshot.CLIRevision != cli.Revision {
		return nil, fmt.Errorf(
			"doctor CLI revision = %q, want %q",
			snapshot.CLIRevision,
			cli.Revision,
		)
	}
	identity, err := targetIdentity(plan.Target)
	if err != nil {
		return nil, err
	}
	if snapshot.Target != identity {
		return nil, errors.New("doctor target identity does not match plan")
	}
	if len(plan.Actions) == 0 || len(plan.Actions) > 512 {
		return nil, fmt.Errorf(
			"plan action count %d is outside the evidence bound",
			len(plan.Actions),
		)
	}
	if len(snapshot.Checks) != len(plan.Actions) {
		return nil, errors.New(
			"bounded doctor verification does not cover every requested action",
		)
	}

	checks := make(map[string]ComponentCheck, len(snapshot.Checks))
	allReady := true
	for _, check := range snapshot.Checks {
		if err := validateComponentCheck(check); err != nil {
			return nil, err
		}
		if _, exists := checks[check.ActionID]; exists {
			return nil, fmt.Errorf("duplicate doctor action %q", check.ActionID)
		}
		checks[check.ActionID] = check
		if check.Status != "ready" {
			allReady = false
		}
	}
	if snapshot.Ready != allReady {
		return nil, errors.New(
			"doctor ready field does not match bounded component outcomes",
		)
	}

	components := make([]ComponentCheck, 0, len(plan.Actions))
	for _, action := range plan.Actions {
		check, exists := checks[action.ID]
		if !exists {
			return nil, fmt.Errorf(
				"doctor verification is missing action %q",
				action.ID,
			)
		}
		if check.ComponentID != action.ComponentID ||
			check.RequestedVersion != action.Version {
			return nil, fmt.Errorf(
				"doctor verification for %q does not match requested component",
				action.ID,
			)
		}
		switch action.Status {
		case planning.ActionPlanned:
			if check.Status == "unsupported" ||
				check.Status == "action-required" {
				return nil, fmt.Errorf(
					"planned action %q has doctor status %q",
					action.ID,
					check.Status,
				)
			}
		case planning.ActionUnsupported:
			if check.Status != "unsupported" ||
				check.ReasonCode != "unsupported" {
				return nil, fmt.Errorf(
					"unsupported action %q has inconsistent doctor outcome",
					action.ID,
				)
			}
		case planning.ActionActionRequired:
			if check.Status != "action-required" ||
				check.ReasonCode != "action-required" {
				return nil, fmt.Errorf(
					"action-required action %q has inconsistent doctor outcome",
					action.ID,
				)
			}
		default:
			return nil, fmt.Errorf(
				"action %q has unknown status %q",
				action.ID,
				action.Status,
			)
		}
		components = append(components, check)
	}
	return components, nil
}

func validatePlanBlockers(plan planning.Plan, targetID string) error {
	blockers := make(map[string]planning.Blocker, len(plan.Blockers))
	for _, blocker := range plan.Blockers {
		if _, exists := blockers[blocker.ActionID]; exists {
			return fmt.Errorf("duplicate plan blocker %q", blocker.ActionID)
		}
		blockers[blocker.ActionID] = blocker
	}
	expectedBlockers := 0
	for _, action := range plan.Actions {
		if action.TargetID != targetID {
			return fmt.Errorf(
				"action %q target = %q, want %q",
				action.ID,
				action.TargetID,
				targetID,
			)
		}
		if !strings.HasPrefix(action.ID, targetID+"/") {
			return fmt.Errorf(
				"action %q is outside target %q",
				action.ID,
				targetID,
			)
		}
		switch action.Status {
		case planning.ActionPlanned:
			if _, exists := blockers[action.ID]; exists {
				return fmt.Errorf(
					"planned action %q must not have a blocker",
					action.ID,
				)
			}
		case planning.ActionUnsupported, planning.ActionActionRequired:
			expectedBlockers++
			blocker, exists := blockers[action.ID]
			if !exists ||
				blocker.Status != action.Status ||
				blocker.Reason != action.Reason {
				return fmt.Errorf(
					"action %q blocker does not match its plan outcome",
					action.ID,
				)
			}
		default:
			return fmt.Errorf(
				"action %q has unknown status %q",
				action.ID,
				action.Status,
			)
		}
	}
	if len(blockers) != expectedBlockers {
		return errors.New("plan contains a blocker for an unknown action")
	}
	return nil
}

func validateComponentCheck(check ComponentCheck) error {
	for label, value := range map[string]string{
		"action_id":         check.ActionID,
		"component_id":      check.ComponentID,
		"status":            check.Status,
		"reason_code":       check.ReasonCode,
		"requested_version": check.RequestedVersion,
		"installed_version": check.InstalledVersion,
		"verified_version":  check.VerifiedVersion,
	} {
		if err := validateBoundedValue(label, value); err != nil {
			return err
		}
	}
	switch check.Status {
	case "ready", "unready", "conflict", "unsupported", "action-required":
	default:
		return fmt.Errorf("unsupported doctor status %q", check.Status)
	}
	if check.Status == "ready" && check.VerifiedVersion == "" {
		return fmt.Errorf(
			"ready component %q has no verified version",
			check.ComponentID,
		)
	}
	if check.Status == "ready" &&
		check.VerifiedVersion != check.InstalledVersion {
		return fmt.Errorf(
			"ready component %q verified version does not match installed version",
			check.ComponentID,
		)
	}
	if check.Status != "ready" && check.VerifiedVersion != "" {
		return fmt.Errorf(
			"non-ready component %q must not claim a verified version",
			check.ComponentID,
		)
	}
	return nil
}

func validateBoundedValue(label, value string) error {
	if value == "" {
		if label == "installed_version" || label == "verified_version" {
			return nil
		}
		return fmt.Errorf("%s is required", label)
	}
	if len(value) > 512 {
		return fmt.Errorf("%s exceeds the evidence length bound", label)
	}
	if strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("%s contains NUL", label)
	}
	return nil
}

func targetIdentity(facts target.Facts) (TargetIdentity, error) {
	id, err := target.NewID(facts.ID.Kind, facts.ID.Name)
	if err != nil {
		return TargetIdentity{}, fmt.Errorf("validate target identity: %w", err)
	}
	fingerprint, err := facts.Fingerprint()
	if err != nil {
		return TargetIdentity{}, fmt.Errorf("fingerprint target: %w", err)
	}
	return TargetIdentity{
		ID: id.String(), Kind: id.Kind, Fingerprint: fingerprint,
	}, nil
}

func targetIdentityComplete(facts target.Facts) bool {
	if facts.OS == "" ||
		facts.OSVersion == "" ||
		facts.Architecture == "" ||
		!facts.Reachable ||
		facts.CLIRevision == "" ||
		facts.CatalogRevision == "" {
		return false
	}
	switch facts.ID.Kind {
	case target.KindMacOSHost:
		return facts.OS == "darwin"
	case target.KindWindowsHost:
		return facts.OS == "windows"
	case target.KindWSLGuest, target.KindLimaGuest:
		return facts.OS == "linux" &&
			facts.RuntimeVersion != "" &&
			facts.ImageRevision != "" &&
			facts.ImageProvenance != "" &&
			facts.ImageCreationNonce != ""
	default:
		return false
	}
}

func parseCLIIdentity(identity CLIIdentity) (CLIIdentity, error) {
	expected, err := parseReleaseCLIRevision(identity.Revision)
	if err != nil {
		return CLIIdentity{}, err
	}
	if identity != expected {
		return CLIIdentity{}, errors.New(
			"manifest CLI fields do not match the exact CLI revision",
		)
	}
	return identity, nil
}

func readBundleFiles(root string) (map[string][]byte, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect evidence directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("evidence directory must not be a symlink")
	}
	if !info.IsDir() {
		return nil, errors.New("evidence path must be a directory")
	}
	if reparse, err := isReparsePoint(root); err != nil {
		return nil, fmt.Errorf("inspect evidence directory reparse state: %w", err)
	} else if reparse {
		return nil, errors.New("evidence directory must not be a reparse point")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read evidence directory: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, expectedFiles) {
		return nil, fmt.Errorf(
			"unexpected evidence file set: got=%v want=%v",
			names,
			expectedFiles,
		)
	}

	files := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		fileInfo, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("inspect evidence file %s: %w", entry.Name(), err)
		}
		if fileInfo.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf(
				"evidence file %s must not be a symlink",
				entry.Name(),
			)
		}
		if !fileInfo.Mode().IsRegular() {
			return nil, fmt.Errorf(
				"evidence file %s must be regular",
				entry.Name(),
			)
		}
		if reparse, err := isReparsePoint(path); err != nil {
			return nil, fmt.Errorf(
				"inspect evidence file %s reparse state: %w",
				entry.Name(),
				err,
			)
		} else if reparse {
			return nil, fmt.Errorf(
				"evidence file %s must not be a reparse point",
				entry.Name(),
			)
		}
		if fileInfo.Size() > maxEvidenceFileSize {
			return nil, fmt.Errorf(
				"evidence file %s exceeds %d bytes",
				entry.Name(),
				maxEvidenceFileSize,
			)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read evidence file %s: %w", entry.Name(), err)
		}
		files[entry.Name()] = content
	}
	return files, nil
}

func verifyChecksums(files map[string][]byte) error {
	expected := renderChecksums(files)
	if !bytes.Equal(files[ChecksumsFile], expected) {
		return errors.New(
			"checksums.txt does not exactly match the evidence payload",
		)
	}
	return nil
}

func renderChecksums(files map[string][]byte) []byte {
	names := append([]string(nil), checksummedFiles...)
	sort.Strings(names)
	var builder strings.Builder
	for _, name := range names {
		sum := sha256.Sum256(files[name])
		builder.WriteString(hex.EncodeToString(sum[:]))
		builder.WriteString("  ")
		builder.WriteString(name)
		builder.WriteByte('\n')
	}
	return []byte(builder.String())
}

func writeBundle(
	outputDir string,
	manifest Manifest,
	plan planning.Plan,
	snapshot DoctorSnapshot,
) (returnErr error) {
	parent := filepath.Dir(outputDir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create evidence parent: %w", err)
	}
	staging, err := os.MkdirTemp(parent, ".mds-target-evidence-*")
	if err != nil {
		return fmt.Errorf("create evidence staging directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(staging); returnErr == nil && err != nil {
			returnErr = fmt.Errorf(
				"remove evidence staging directory: %w",
				err,
			)
		}
	}()
	if err := os.Chmod(staging, 0o700); err != nil {
		return fmt.Errorf("protect evidence staging directory: %w", err)
	}
	files := make(map[string][]byte, len(checksummedFiles)+1)
	for name, value := range map[string]any{
		ManifestFile: manifest,
		PlanFile:     plan,
		DoctorFile:   snapshot,
	} {
		content, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return fmt.Errorf("encode %s: %w", name, err)
		}
		files[name] = append(content, '\n')
	}
	files[ChecksumsFile] = renderChecksums(files)
	for _, name := range expectedFiles {
		if err := os.WriteFile(
			filepath.Join(staging, name),
			files[name],
			0o600,
		); err != nil {
			return fmt.Errorf("write evidence file %s: %w", name, err)
		}
	}
	if err := durable.PublishDirectory(staging, outputDir); err != nil {
		return fmt.Errorf("publish evidence directory: %w", err)
	}
	return nil
}

func decodeStrict(content []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}
