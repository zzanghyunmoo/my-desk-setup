package catalog

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	exactartifact "github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
)

const OfficialNPMRegistry = "https://registry.npmjs.org"

func CanonicalNPMTarballURL(
	registryBase,
	packageName,
	version string,
) (string, error) {
	if packageName == "" || packageName != strings.TrimSpace(packageName) ||
		version == "" || version != strings.TrimSpace(version) {
		return "", errors.New("npm package and version must be non-empty without surrounding whitespace")
	}
	if strings.ContainsAny(packageName, `\?#`) ||
		strings.ContainsAny(version, `/\?#`) {
		return "", errors.New("npm package or version contains unsafe URL characters")
	}
	segments := strings.Split(packageName, "/")
	if len(segments) > 2 || len(segments) == 2 && !strings.HasPrefix(segments[0], "@") {
		return "", fmt.Errorf("invalid npm package name %q", packageName)
	}
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("invalid npm package name %q", packageName)
		}
	}
	registry, err := url.ParseRequestURI(strings.TrimRight(registryBase, "/"))
	if err != nil || registry.Scheme != "https" || registry.Host == "" ||
		registry.User != nil || registry.RawQuery != "" || registry.Fragment != "" {
		return "", errors.New("npm registry must be an absolute canonical HTTPS URL")
	}
	name := path.Base(packageName)
	registry.Path = strings.TrimRight(registry.Path, "/") + "/" +
		packageName + "/-/" + name + "-" + version + ".tgz"
	registry.RawPath = ""
	return registry.String(), nil
}

func validateNPMArtifact(
	artifact NPMArtifact,
	packageName,
	version string,
) error {
	expected, err := CanonicalNPMTarballURL(
		OfficialNPMRegistry,
		packageName,
		version,
	)
	if err != nil {
		return err
	}
	if artifact.Tarball != expected {
		return fmt.Errorf(
			"npm tarball must be canonical: expected %s",
			expected,
		)
	}
	if _, err := exactartifact.DecodeSHA512SRI(artifact.Integrity); err != nil {
		return fmt.Errorf("npm artifact SRI: %w", err)
	}
	if err := exactartifact.ValidateSHA256(artifact.SHA256); err != nil {
		return fmt.Errorf("npm artifact digest: %w", err)
	}
	return nil
}
