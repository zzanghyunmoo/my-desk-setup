package artifact_test

import (
	"bytes"
	"crypto/sha512"
	"encoding/base64"
	"io"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
)

func TestCopyAndVerifyRejectsOversizedArtifact(t *testing.T) {
	content := []byte("larger than the fixture limit")
	sum := sha512.Sum512(content)
	integrity := "sha512-" + base64.StdEncoding.EncodeToString(sum[:])

	_, err := artifact.CopyAndVerify(
		bytes.NewReader(content),
		io.Discard,
		"",
		integrity,
		8,
	)
	if err == nil || !strings.Contains(err.Error(), "exceeds 8 bytes") {
		t.Fatalf("CopyAndVerify() error = %v", err)
	}
}
