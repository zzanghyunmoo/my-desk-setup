package target

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

type limaRecord struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Arch    string `json:"arch"`
	Version string `json:"limaVersion"`
}

type Environment interface {
	Getenv(string) string
}

type GetenvFunc func(string) string

func (function GetenvFunc) Getenv(key string) string {
	return function(key)
}

func DiscoverLocal(
	goos,
	goarch string,
	environment Environment,
) (Facts, error) {
	imageRevision := strings.TrimSpace(environment.Getenv("MDS_IMAGE_REVISION"))
	imageProvenance := strings.TrimSpace(environment.Getenv("MDS_IMAGE_PROVENANCE"))
	imageCreationNonce := strings.TrimSpace(
		environment.Getenv("MDS_IMAGE_CREATION_NONCE"),
	)
	switch goos {
	case "darwin":
		id, _ := NewID(KindMacOSHost, "local")
		return Facts{ID: id, OS: "darwin", Architecture: goarch, Reachable: true}, nil
	case "windows":
		id, _ := NewID(KindWindowsHost, "local")
		return Facts{ID: id, OS: "windows", Architecture: goarch, Reachable: true}, nil
	case "linux":
		if distro := strings.TrimSpace(environment.Getenv("WSL_DISTRO_NAME")); distro != "" {
			id, err := NewID(KindWSLGuest, distro)
			if err != nil {
				return Facts{}, err
			}
			return Facts{
				ID: id, OS: "linux", Architecture: goarch, Reachable: true,
				ImageRevision: imageRevision, ImageProvenance: imageProvenance,
				ImageCreationNonce: imageCreationNonce,
			}, nil
		}
		if instance := strings.TrimSpace(environment.Getenv("LIMA_INSTANCE")); instance != "" {
			id, err := NewID(KindLimaGuest, instance)
			if err != nil {
				return Facts{}, err
			}
			return Facts{
				ID: id, OS: "linux", Architecture: goarch, Reachable: true,
				ImageRevision: imageRevision, ImageProvenance: imageProvenance,
				ImageCreationNonce: imageCreationNonce,
			}, nil
		}
		return Facts{}, errors.New("native Linux is not a v1 target; use a WSL or Lima guest")
	default:
		return Facts{}, fmt.Errorf("unsupported operating system %q", goos)
	}
}

func Select(candidates []Facts, explicit string) (Facts, error) {
	if explicit != "" {
		expected, err := ParseID(explicit)
		if err != nil {
			return Facts{}, err
		}
		for _, candidate := range candidates {
			if candidate.ID == expected {
				return candidate, nil
			}
		}
		return Facts{}, fmt.Errorf("target %q was not discovered", explicit)
	}
	switch len(candidates) {
	case 0:
		return Facts{}, errors.New("no targets discovered")
	case 1:
		return candidates[0], nil
	default:
		ids := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			ids = append(ids, candidate.ID.String())
		}
		sort.Strings(ids)
		return Facts{}, fmt.Errorf(
			"multiple targets discovered; choose one explicitly: %s",
			strings.Join(ids, ", "),
		)
	}
}

func ParseWSLDistributions(output []byte) ([]Facts, error) {
	normalized := strings.ReplaceAll(string(output), "\x00", "")
	var facts []Facts
	seen := make(map[string]bool)
	for _, line := range strings.Split(normalized, "\n") {
		name := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		name = strings.TrimSuffix(name, " (Default)")
		if name == "" || strings.EqualFold(name, "NAME") || seen[name] {
			continue
		}
		id, err := NewID(KindWSLGuest, name)
		if err != nil {
			return nil, fmt.Errorf("parse WSL distribution %q: %w", name, err)
		}
		seen[name] = true
		facts = append(facts, Facts{ID: id, OS: "linux", Reachable: true})
	}
	sort.Slice(facts, func(left, right int) bool {
		return facts[left].ID.String() < facts[right].ID.String()
	})
	return facts, nil
}

func ParseLimaInstances(output []byte) ([]Facts, error) {
	output = bytes.TrimSpace(output)
	if len(output) == 0 {
		return nil, nil
	}

	var records []limaRecord
	if output[0] == '[' {
		if err := json.Unmarshal(output, &records); err != nil {
			return nil, fmt.Errorf("parse Lima instance list: %w", err)
		}
	} else {
		decoder := json.NewDecoder(bytes.NewReader(output))
		for {
			var record limaRecord
			err := decoder.Decode(&record)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("parse Lima instance record: %w", err)
			}
			records = append(records, record)
		}
	}

	facts := make([]Facts, 0, len(records))
	for _, record := range records {
		id, err := NewID(KindLimaGuest, record.Name)
		if err != nil {
			return nil, fmt.Errorf("parse Lima instance %q: %w", record.Name, err)
		}
		facts = append(facts, Facts{
			ID:             id,
			OS:             "linux",
			Architecture:   record.Arch,
			RuntimeVersion: record.Version,
			Reachable:      strings.EqualFold(record.Status, "Running"),
		})
	}
	sort.Slice(facts, func(left, right int) bool {
		return facts[left].ID.String() < facts[right].ID.String()
	})
	return facts, nil
}
