package packages

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/managedfile"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/durable"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

const (
	miseOwnershipSchema = "mds.mise-ownership/v1"
	miseOwnershipFile   = ".mds-managed.json"
)

type miseOwnership struct {
	SchemaVersion string `json:"schema_version"`
	ConfigSHA256  string `json:"config_sha256"`
	LockSHA256    string `json:"lock_sha256"`
}

var legacyManagedMisePairs = map[string]bool{
	"05535710109596ccf5c93448f7813a9b1703ec2431fa767da3536ed7a2c98fe4:" +
		"99c7f6d97b4b1b0630c36ae1d57d612c6f2abeda36f78ca05ef99ea3b4ae7725": true,
}

func MiseInstall(action planning.Action, environment map[string]string) ([]transport.Command, error) {
	if action.Package == "" || action.Version == "" ||
		action.Version == "manager-owned" || action.Version == "manual" {
		return nil, fmt.Errorf("mise requires an exact package and version for %s", action.ID)
	}
	if action.Inputs["artifact_sha256"] == "" ||
		action.Inputs["artifact_url"] == "" ||
		action.Inputs["mise_ref"] == "" {
		return nil, fmt.Errorf(
			"mise requires reviewed artifact identity and install ref for %s",
			action.ID,
		)
	}
	return []transport.Command{
		{
			Executable: "mise",
			Arguments: []string{
				"install", "--locked", action.Package,
			},
			Environment: environment,
			Timeout:     45 * time.Minute,
		},
		{
			Executable:  "mise",
			Arguments:   []string{"reshim"},
			Environment: environment,
			Timeout:     5 * time.Minute,
		},
	}, nil
}

func PublishMiseConfig(home string, mise catalog.MiseFiles) error {
	if home == "" {
		return fmt.Errorf("home directory is required for mise configuration")
	}
	if mise.Config == "" || mise.Lock == "" {
		return errors.New("reviewed mise.toml and mise.lock content is required")
	}
	directory := filepath.Join(home, ".config", "mise")
	configPath := filepath.Join(directory, "config.toml")
	lockPath := filepath.Join(directory, "mise.lock")
	markerPath := filepath.Join(directory, miseOwnershipFile)
	current := miseOwnership{
		SchemaVersion: miseOwnershipSchema,
		ConfigSHA256:  miseContentSHA256(mise.Config),
		LockSHA256:    miseContentSHA256(mise.Lock),
	}
	marker, err := json.Marshal(current)
	if err != nil {
		return fmt.Errorf("encode managed mise ownership: %w", err)
	}
	marker = append(marker, '\n')

	configInspection := managedfile.Inspect(configPath, mise.Config)
	lockInspection := managedfile.Inspect(lockPath, mise.Lock)
	if configInspection.State == managedfile.StateReady && lockInspection.State == managedfile.StateReady {
		return publishMiseMarker(markerPath, marker, current)
	}
	if configInspection.State == managedfile.StateMissing && lockInspection.State == managedfile.StateMissing {
		if err := managedfile.Publish(lockPath, mise.Lock); err != nil {
			return fmt.Errorf("publish managed mise mise.lock: %w", err)
		}
		if err := managedfile.Publish(configPath, mise.Config); err != nil {
			return fmt.Errorf("publish managed mise mise.toml: %w", err)
		}
		return publishMiseMarker(markerPath, marker, current)
	}
	owned, err := managedMiseUpgradeAllowed(configPath, lockPath, markerPath, current)
	if err != nil {
		return err
	}
	if !owned {
		return errors.New("existing managed mise files are user-owned; they will not be overwritten")
	}

	// Publish the lock first so an interruption cannot expose a new config
	// without its matching lock. A lock without the managed config is inert
	// and the ownership marker makes a partially published pair resumable.
	if err := durable.WriteFile(lockPath, []byte(mise.Lock), 0o700); err != nil {
		return fmt.Errorf("replace managed mise mise.lock: %w", err)
	}
	if err := durable.WriteFile(configPath, []byte(mise.Config), 0o700); err != nil {
		return fmt.Errorf("replace managed mise mise.toml: %w", err)
	}
	return durable.WriteFile(markerPath, marker, 0o600)
}

func managedMiseUpgradeAllowed(configPath, lockPath, markerPath string, current miseOwnership) (bool, error) {
	config, err := readRegularMiseFile(configPath)
	if err != nil {
		return false, fmt.Errorf("inspect managed mise mise.toml: %w", err)
	}
	lock, err := readRegularMiseFile(lockPath)
	if err != nil {
		return false, fmt.Errorf("inspect managed mise mise.lock: %w", err)
	}
	configSHA := miseContentSHA256(string(config))
	lockSHA := miseContentSHA256(string(lock))
	marker, markerErr := readMiseOwnership(markerPath)
	if errors.Is(markerErr, os.ErrNotExist) {
		return legacyManagedMisePairs[configSHA+":"+lockSHA], nil
	}
	if markerErr != nil {
		return false, markerErr
	}
	if marker.SchemaVersion != miseOwnershipSchema {
		return false, nil
	}
	configOwned := configSHA == marker.ConfigSHA256 || configSHA == current.ConfigSHA256
	lockOwned := lockSHA == marker.LockSHA256 || lockSHA == current.LockSHA256
	return configOwned && lockOwned, nil
}

func publishMiseMarker(path string, encoded []byte, expected miseOwnership) error {
	marker, err := readMiseOwnership(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return durable.WriteFileNoReplace(path, encoded, 0o600)
	case err != nil:
		return err
	case marker == expected:
		return nil
	default:
		return errors.New("managed mise ownership marker conflicts with exact files")
	}
}

func readMiseOwnership(path string) (miseOwnership, error) {
	content, err := readRegularMiseFile(path)
	if err != nil {
		return miseOwnership{}, err
	}
	var marker miseOwnership
	if json.Unmarshal(content, &marker) != nil ||
		len(marker.ConfigSHA256) != sha256.Size*2 || len(marker.LockSHA256) != sha256.Size*2 {
		return miseOwnership{}, errors.New("managed mise ownership marker is invalid")
	}
	if _, err := hex.DecodeString(marker.ConfigSHA256); err != nil {
		return miseOwnership{}, errors.New("managed mise ownership config digest is invalid")
	}
	if _, err := hex.DecodeString(marker.LockSHA256); err != nil {
		return miseOwnership{}, errors.New("managed mise ownership lock digest is invalid")
	}
	return marker, nil
}

func readRegularMiseFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("managed mise path is not a regular file")
	}
	return os.ReadFile(path)
}

func miseContentSHA256(content string) string {
	return digestBytes([]byte(content))
}
