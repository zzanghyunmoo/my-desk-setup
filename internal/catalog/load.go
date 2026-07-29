package catalog

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func Load(root string) (Environment, error) {
	return LoadFS(os.DirFS(root))
}

func LoadFS(filesystem fs.FS) (Environment, error) {
	var environment Environment
	environment.Catalog.SchemaVersion = 1
	environment.Profiles = make(map[string]Profile)

	componentPaths, err := fs.Glob(filesystem, "components/*.yaml")
	if err != nil {
		return Environment{}, fmt.Errorf("find component documents: %w", err)
	}
	sort.Strings(componentPaths)
	if len(componentPaths) == 0 {
		return Environment{}, errors.New("catalog has no component documents")
	}
	for _, path := range componentPaths {
		var document Catalog
		if err := decodeStrictFS(filesystem, path, &document); err != nil {
			return Environment{}, err
		}
		if document.SchemaVersion != 1 {
			return Environment{}, fmt.Errorf("%s: unsupported schema_version %d", path, document.SchemaVersion)
		}
		environment.Catalog.Components = append(
			environment.Catalog.Components,
			document.Components...,
		)
	}

	profilePaths, err := fs.Glob(filesystem, "profiles/*.yaml")
	if err != nil {
		return Environment{}, fmt.Errorf("find profiles: %w", err)
	}
	sort.Strings(profilePaths)
	for _, path := range profilePaths {
		var profile Profile
		if err := decodeStrictFS(filesystem, path, &profile); err != nil {
			return Environment{}, err
		}
		if profile.ID == "" {
			return Environment{}, fmt.Errorf("%s: profile id is required", path)
		}
		if expected := profileIDFromPath(path); profile.ID != expected {
			return Environment{}, fmt.Errorf(
				"%s: profile id %q must match filename %q",
				path,
				profile.ID,
				expected,
			)
		}
		if _, exists := environment.Profiles[profile.ID]; exists {
			return Environment{}, fmt.Errorf("duplicate profile id %q", profile.ID)
		}
		environment.Profiles[profile.ID] = profile
	}

	lockPath := "locks/versions.lock.yaml"
	if err := decodeStrictFS(filesystem, lockPath, &environment.Lock); err != nil {
		return Environment{}, err
	}
	if err := Validate(environment); err != nil {
		return Environment{}, err
	}
	return environment, nil
}

func decodeStrictFS(filesystem fs.FS, path string, target any) error {
	file, err := filesystem.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("decode %s: multiple YAML documents are not allowed", path)
		}
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func CatalogRoot(repositoryRoot string) string {
	return filepath.Join(repositoryRoot, "catalog")
}

func profileIDFromPath(path string) string {
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}
