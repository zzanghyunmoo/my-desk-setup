package guest

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
	"github.com/zzanghyunmoo/my-desk-setup/internal/durable"
)

const (
	OwnershipSchemaVersion = "mds.guest-ownership/v3"
	OwnershipPreparing     = "preparing"
	OwnershipCommitted     = "committed"
)

// Ownership records the exact catalog image selected by the host when it
// creates a guest. Its presence is the boundary between mds-managed guests and
// pre-existing user-owned guests that happen to use the same name.
type Ownership struct {
	SchemaVersion string `json:"schema_version"`
	Provider      string `json:"provider"`
	Name          string `json:"name"`
	ImageURL      string `json:"image_url"`
	ImageSHA256   string `json:"image_sha256"`
	CreationNonce string `json:"creation_nonce"`
	Phase         string `json:"phase"`
}

func LoadOwnership(
	root,
	provider,
	name string,
) (record Ownership, exists bool, resultErr error) {
	path, err := ownershipPath(root, provider, name)
	if err != nil {
		return Ownership{}, false, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Ownership{}, false, nil
	}
	if err != nil {
		return Ownership{}, false, fmt.Errorf("inspect guest ownership record: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Ownership{}, false, errors.New("guest ownership record is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return Ownership{}, false, fmt.Errorf("open guest ownership record: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("close guest ownership record: %w", closeErr),
			)
		}
	}()

	decoder := json.NewDecoder(io.LimitReader(file, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return Ownership{}, false, fmt.Errorf("decode guest ownership record: %w", err)
	}
	if record.SchemaVersion != OwnershipSchemaVersion ||
		record.Provider != provider ||
		record.Name != name ||
		strings.TrimSpace(record.ImageURL) == "" ||
		strings.TrimSpace(record.ImageSHA256) == "" ||
		artifact.ValidateSHA256(record.CreationNonce) != nil ||
		(record.Phase != OwnershipPreparing &&
			record.Phase != OwnershipCommitted) {
		return Ownership{}, false, errors.New("guest ownership record identity is invalid")
	}
	return record, true, nil
}

func PublishOwnership(root string, record Ownership) error {
	var err error
	record, err = withCreationNonce(record)
	if err != nil {
		return err
	}
	record.Phase = OwnershipCommitted
	return publishOwnership(root, record, true)
}

func PrepareOwnership(root string, record Ownership) (Ownership, error) {
	var err error
	record, err = withCreationNonce(record)
	if err != nil {
		return Ownership{}, err
	}
	record.Phase = OwnershipPreparing
	if err := publishOwnership(root, record, false); err != nil {
		return Ownership{}, err
	}
	return record, nil
}

func CommitOwnership(root string, record Ownership) error {
	current, exists, err := LoadOwnership(
		root,
		record.Provider,
		record.Name,
	)
	if err != nil {
		return err
	}
	if !exists ||
		current.Phase != OwnershipPreparing ||
		!sameOwnershipIdentity(current, record) {
		return errors.New(
			"guest ownership can be committed only from the matching preparing record",
		)
	}
	record.Phase = OwnershipCommitted
	return publishOwnership(root, record, false)
}

func publishOwnership(root string, record Ownership, noReplace bool) error {
	path, err := ownershipPath(root, record.Provider, record.Name)
	if err != nil {
		return err
	}
	record.SchemaVersion = OwnershipSchemaVersion
	if strings.TrimSpace(record.ImageURL) == "" ||
		strings.TrimSpace(record.ImageSHA256) == "" ||
		artifact.ValidateSHA256(record.CreationNonce) != nil ||
		(record.Phase != OwnershipPreparing &&
			record.Phase != OwnershipCommitted) {
		return errors.New(
			"guest ownership image URL, SHA-256, and creation nonce are required",
		)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create guest ownership directory: %w", err)
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode guest ownership record: %w", err)
	}
	encoded = append(encoded, '\n')
	write := durable.WriteFile
	if noReplace {
		write = durable.WriteFileNoReplace
	}
	if err := write(path, encoded, 0o600); err != nil {
		return fmt.Errorf("publish guest ownership record: %w", err)
	}
	return nil
}

func sameOwnershipIdentity(left, right Ownership) bool {
	return left.Provider == right.Provider &&
		left.Name == right.Name &&
		left.ImageURL == right.ImageURL &&
		left.ImageSHA256 == right.ImageSHA256 &&
		left.CreationNonce == right.CreationNonce
}

func withCreationNonce(record Ownership) (Ownership, error) {
	if record.CreationNonce != "" {
		if artifact.ValidateSHA256(record.CreationNonce) != nil {
			return Ownership{}, errors.New(
				"guest ownership creation nonce must contain exactly 64 lowercase hex characters",
			)
		}
		return record, nil
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return Ownership{}, fmt.Errorf("generate guest ownership creation nonce: %w", err)
	}
	record.CreationNonce = hex.EncodeToString(nonce)
	return record, nil
}

func ownershipPath(root, provider, name string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("guest ownership root is required")
	}
	for label, value := range map[string]string{
		"provider": provider,
		"name":     name,
	} {
		if value == "" ||
			value != filepath.Base(value) ||
			strings.ContainsAny(value, `/\\`+"\r\n\x00") {
			return "", fmt.Errorf("valid guest ownership %s is required", label)
		}
	}
	return filepath.Join(root, provider+"-"+name+".json"), nil
}
