package evidence

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	credentialMaterialPattern = regexp.MustCompile(
		`(?i)(api[_-]?key|access[_-]?token|token|password|secret|credential|authorization|cookie)["'\s]*[:=]["'\s]*[^"',}\]\s]+`,
	)
	authCommandPattern = regexp.MustCompile(
		`(?i)"(?:auth|login|logout)"`,
	)
	credentialFlagPattern = regexp.MustCompile(
		`(?i)"--(?:api-key|access-token|token|password|secret|credential)(?:=|")`,
	)
	rawGuestNonceFieldPattern = regexp.MustCompile(
		`(?i)"(?:image_)?creation_nonce"\s*:`,
	)
	unixHomePattern = regexp.MustCompile(
		`(?i)(?:/Users/[^/"'\s]+|/home/[^/"'\s]+|/root)(?:[/\\]|["'\s]|$)`,
	)
	windowsHomePattern = regexp.MustCompile(
		`(?i)[a-z]:\\Users\\[^\\/"'\s]+(?:\\|["'\s]|$)`,
	)
)

func scanEvidenceMaterial(name string, content []byte) error {
	if len(content) > maxEvidenceFileSize {
		return fmt.Errorf("%s exceeds the evidence size bound", name)
	}
	if strings.ContainsRune(string(content), '\x00') {
		return fmt.Errorf("%s contains NUL", name)
	}
	text := string(content)
	if credentialMaterialPattern.MatchString(text) {
		return fmt.Errorf("%s contains credential-shaped material", name)
	}
	normalizedSlashes := strings.ReplaceAll(text, `\\`, `\`)
	if unixHomePattern.MatchString(text) ||
		windowsHomePattern.MatchString(normalizedSlashes) {
		return fmt.Errorf("%s contains a personal absolute home path", name)
	}
	if authCommandPattern.MatchString(text) {
		return fmt.Errorf("%s contains an authentication command", name)
	}
	if credentialFlagPattern.MatchString(text) {
		return fmt.Errorf("%s contains a credential flag", name)
	}
	if rawGuestNonceFieldPattern.MatchString(text) {
		return fmt.Errorf("%s contains a raw guest creation nonce field", name)
	}
	return nil
}
