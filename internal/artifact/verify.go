package artifact

import (
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var sha256Pattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

const MaxDownloadBytes int64 = 512 << 20

func ValidateSHA256(value string) error {
	if !sha256Pattern.MatchString(value) {
		return errors.New("SHA-256 must be 64 lowercase hexadecimal characters")
	}
	return nil
}

func DecodeSHA512SRI(value string) ([]byte, error) {
	if value == "" || value != strings.TrimSpace(value) ||
		strings.ContainsAny(value, " \t\r\n") {
		return nil, errors.New("SRI must be one canonical sha512 token")
	}
	const prefix = "sha512-"
	if !strings.HasPrefix(value, prefix) {
		return nil, errors.New("SRI must use sha512")
	}
	encoded := strings.TrimPrefix(value, prefix)
	digest, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(digest) != sha512.Size ||
		base64.StdEncoding.EncodeToString(digest) != encoded {
		return nil, errors.New("SRI must contain one canonical SHA-512 digest")
	}
	return digest, nil
}

func CopyAndVerify(
	reader io.Reader,
	writer io.Writer,
	expectedSHA256,
	integrity string,
	maxBytes int64,
) (string, error) {
	if reader == nil {
		return "", errors.New("artifact reader is required")
	}
	if writer == nil {
		writer = io.Discard
	}
	if maxBytes <= 0 {
		return "", errors.New("artifact size limit must be positive")
	}
	expectedSRI, err := DecodeSHA512SRI(integrity)
	if err != nil {
		return "", err
	}
	if expectedSHA256 != "" {
		if err := ValidateSHA256(expectedSHA256); err != nil {
			return "", err
		}
	}
	sha256Hash := sha256.New()
	sha512Hash := sha512.New()
	written, err := io.Copy(
		io.MultiWriter(writer, sha256Hash, sha512Hash),
		io.LimitReader(reader, maxBytes+1),
	)
	if err != nil {
		return "", fmt.Errorf("copy artifact: %w", err)
	}
	if written > maxBytes {
		return "", fmt.Errorf("artifact exceeds %d bytes", maxBytes)
	}
	actualSRI := sha512Hash.Sum(nil)
	if subtle.ConstantTimeCompare(actualSRI, expectedSRI) != 1 {
		return "", errors.New("artifact integrity mismatch")
	}
	actualSHA256 := hex.EncodeToString(sha256Hash.Sum(nil))
	if expectedSHA256 != "" && actualSHA256 != expectedSHA256 {
		return "", fmt.Errorf(
			"artifact digest mismatch: expected %s got %s",
			expectedSHA256,
			actualSHA256,
		)
	}
	return actualSHA256, nil
}
