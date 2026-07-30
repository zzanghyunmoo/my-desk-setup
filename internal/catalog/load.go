package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

func Load(root string) (Environment, error) {
	return LoadFS(os.DirFS(root))
}

func LoadFS(filesystem fs.FS) (Environment, error) {
	var environment Environment
	environment.Catalog.SchemaVersion = 1
	environment.Profiles = make(map[string]Profile)
	environment.Targets = make(map[string]TargetSpec)

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
		if err := validatePublishedSchema(
			filesystem,
			"schema/environment.schema.json",
			document,
		); err != nil {
			return Environment{}, fmt.Errorf("%s: %w", path, err)
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

	targetPaths, err := fs.Glob(filesystem, "targets/*.yaml")
	if err != nil {
		return Environment{}, fmt.Errorf("find target documents: %w", err)
	}
	sort.Strings(targetPaths)
	for _, path := range targetPaths {
		var specification TargetSpec
		if err := decodeStrictFS(filesystem, path, &specification); err != nil {
			return Environment{}, err
		}
		if specification.ID == "" {
			return Environment{}, fmt.Errorf("%s: target id is required", path)
		}
		if expected := profileIDFromPath(path); specification.ID != expected {
			return Environment{}, fmt.Errorf(
				"%s: target id %q must match filename %q",
				path,
				specification.ID,
				expected,
			)
		}
		if _, exists := environment.Targets[specification.ID]; exists {
			return Environment{}, fmt.Errorf(
				"duplicate target id %q",
				specification.ID,
			)
		}
		environment.Targets[specification.ID] = specification
	}

	lockPath := "locks/versions.lock.yaml"
	if err := decodeStrictFS(filesystem, lockPath, &environment.Lock); err != nil {
		return Environment{}, err
	}
	if err := validatePublishedSchema(
		filesystem,
		"schema/lock.schema.json",
		environment.Lock,
	); err != nil {
		return Environment{}, fmt.Errorf("%s: %w", lockPath, err)
	}
	miseConfig, err := readBoundedFS(filesystem, "mise.toml", 1<<20)
	if err != nil {
		return Environment{}, err
	}
	miseLock, err := readBoundedFS(filesystem, "mise.lock", 4<<20)
	if err != nil {
		return Environment{}, err
	}
	environment.Mise = MiseFiles{
		Config: normalizeTextLineEndings(string(miseConfig)),
		Lock:   normalizeTextLineEndings(string(miseLock)),
	}
	if err := Validate(environment); err != nil {
		return Environment{}, err
	}
	return environment, nil
}

func normalizeTextLineEndings(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	return strings.ReplaceAll(content, "\r", "\n")
}

func validatePublishedSchema(
	filesystem fs.FS,
	path string,
	document any,
) error {
	content, err := readBoundedFS(filesystem, path, 1<<20)
	if err != nil {
		return fmt.Errorf("load published schema: %w", err)
	}
	var schemaDocument any
	if err := json.Unmarshal(content, &schemaDocument); err != nil {
		return fmt.Errorf("decode published schema %s: %w", path, err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(path, schemaDocument); err != nil {
		return fmt.Errorf("register published schema %s: %w", path, err)
	}
	compiled, err := compiler.Compile(path)
	if err != nil {
		return fmt.Errorf("compile published schema %s: %w", path, err)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode catalog document for schema validation: %w", err)
	}
	var instance any
	if err := json.Unmarshal(encoded, &instance); err != nil {
		return fmt.Errorf("decode catalog document for schema validation: %w", err)
	}
	if err := compiled.Validate(instance); err != nil {
		return fmt.Errorf("published schema %s rejected document: %w", path, err)
	}
	return nil
}

func readBoundedFS(filesystem fs.FS, path string, limit int64) ([]byte, error) {
	file, err := filesystem.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() {
		_ = file.Close()
	}()
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, limit)
	}
	return content, nil
}

func decodeStrictFS(
	filesystem fs.FS,
	path string,
	target any,
) (resultErr error) {
	file, err := filesystem.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("close %s: %w", path, err),
			)
		}
	}()

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
