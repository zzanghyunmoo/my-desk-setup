package release

import (
	"context"
	"fmt"
	"path/filepath"

	exactartifact "github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
)

type Certifier struct {
	Name         string `json:"name"`
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
}

func stageCertifier(
	ctx context.Context,
	sourceRoot string,
	staging string,
	releaseTarget target,
	manifest Manifest,
) (Certifier, error) {
	name := certifierName(manifest.Version, releaseTarget)
	path := filepath.Join(staging, name)
	if err := buildExecutable(
		ctx,
		sourceRoot,
		path,
		releaseTarget,
		manifest,
		"./cmd/mds-evidence",
		false,
	); err != nil {
		return Certifier{}, err
	}
	digest, size, err := fileIdentity(path)
	if err != nil {
		return Certifier{}, err
	}
	return Certifier{
		Name: name, OS: releaseTarget.os,
		Architecture: releaseTarget.architecture,
		SHA256:       digest, Size: size,
	}, nil
}

func certifierName(version string, releaseTarget target) string {
	extension := ""
	if releaseTarget.os == "windows" {
		extension = ".exe"
	}
	return fmt.Sprintf(
		"mds-evidence_%s_%s_%s%s",
		version,
		releaseTarget.os,
		releaseTarget.architecture,
		extension,
	)
}

func validateCertifiers(manifest Manifest) error {
	if len(manifest.Certifiers) != len(releaseTargets) {
		return fmt.Errorf(
			"manifest has %d certifiers, want %d",
			len(manifest.Certifiers),
			len(releaseTargets),
		)
	}
	for index, releaseTarget := range releaseTargets {
		certifier := manifest.Certifiers[index]
		if certifier.Name != certifierName(manifest.Version, releaseTarget) ||
			certifier.OS != releaseTarget.os ||
			certifier.Architecture != releaseTarget.architecture {
			return fmt.Errorf(
				"manifest certifier %d does not match expected %s/%s identity",
				index,
				releaseTarget.os,
				releaseTarget.architecture,
			)
		}
		if exactartifact.ValidateSHA256(certifier.SHA256) != nil || certifier.Size <= 0 {
			return fmt.Errorf(
				"manifest certifier %q has invalid file identity",
				certifier.Name,
			)
		}
	}
	return nil
}
