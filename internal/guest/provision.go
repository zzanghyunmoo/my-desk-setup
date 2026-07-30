package guest

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"gopkg.in/yaml.v3"
)

type Spec = catalog.TargetSpec
type ImageSpec = catalog.ImageSpec

func LoadSpec(path string) (spec Spec, resultErr error) {
	file, err := os.Open(path)
	if err != nil {
		return Spec{}, fmt.Errorf("open guest spec: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			spec = Spec{}
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("close guest spec: %w", err),
			)
		}
	}()
	return decodeSpec(file)
}

func LoadSpecFS(filesystem fs.FS, path string) (spec Spec, resultErr error) {
	file, err := filesystem.Open(path)
	if err != nil {
		return Spec{}, fmt.Errorf("open guest spec: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			spec = Spec{}
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("close guest spec: %w", err),
			)
		}
	}()
	return decodeSpec(file)
}

func decodeSpec(reader io.Reader) (Spec, error) {
	var spec Spec
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)
	if err := decoder.Decode(&spec); err != nil {
		return Spec{}, fmt.Errorf("decode guest spec: %w", err)
	}
	if spec.SchemaVersion != 1 || spec.ID == "" || spec.Distribution == "" || spec.Release == "" {
		return Spec{}, errors.New("guest spec requires schema_version 1, id, distribution, and release")
	}
	for architecture, image := range spec.Images {
		if image.URL == "" || len(image.SHA256) != 64 {
			return Spec{}, fmt.Errorf("guest image %q requires URL and SHA-256", architecture)
		}
	}
	for architecture, image := range spec.WSLImages {
		if image.URL == "" || len(image.SHA256) != 64 {
			return Spec{}, fmt.Errorf("WSL image %q requires URL and SHA-256", architecture)
		}
	}
	return spec, nil
}
