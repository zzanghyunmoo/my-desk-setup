package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
)

const defaultNPMRegistry = "https://registry.npmjs.org"

type npmLatest struct {
	Version string `json:"version"`
	Dist    struct {
		Integrity string `json:"integrity"`
		Tarball   string `json:"tarball"`
	} `json:"dist"`
}

func Discover(
	ctx context.Context,
	environment catalog.Environment,
	targetKind catalog.TargetKind,
	componentID string,
	client *http.Client,
	registryBase string,
) (Candidate, error) {
	component, support, err := supportedComponent(environment, targetKind, componentID)
	if err != nil {
		return Candidate{}, err
	}
	if component.VersionPolicy.Mode != "pinned" {
		return Candidate{}, fmt.Errorf(
			"component %q is %s-owned; no exact candidate discovery is available",
			componentID,
			component.VersionPolicy.Mode,
		)
	}
	if support.Installer != "bun" {
		return Candidate{}, fmt.Errorf(
			"component %q requires a reviewed candidate file with exact provenance and artifacts",
			componentID,
		)
	}
	return discoverNPM(ctx, componentID, support.Package, client, registryBase)
}

func discoverNPM(
	ctx context.Context,
	componentID,
	packageName string,
	client *http.Client,
	registryBase string,
) (Candidate, error) {
	if packageName == "" {
		return Candidate{}, errors.New("npm package name is required")
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if registryBase == "" {
		registryBase = defaultNPMRegistry
	}
	endpoint := strings.TrimRight(registryBase, "/") + "/" +
		url.PathEscape(packageName) + "/latest"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Candidate{}, fmt.Errorf("create npm latest request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return Candidate{}, fmt.Errorf("discover npm candidate: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		return Candidate{}, errors.New("npm candidate discovery was rate limited")
	}
	if response.StatusCode != http.StatusOK {
		return Candidate{}, fmt.Errorf(
			"discover npm candidate: HTTP %s",
			response.Status,
		)
	}
	var latest npmLatest
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&latest); err != nil {
		return Candidate{}, fmt.Errorf("decode npm candidate: %w", err)
	}
	if latest.Version == "" || latest.Dist.Integrity == "" || latest.Dist.Tarball == "" {
		return Candidate{}, errors.New(
			"npm candidate is missing version, integrity, or tarball provenance",
		)
	}
	if latest.Version != strings.TrimSpace(latest.Version) {
		return Candidate{}, errors.New("npm candidate version contains surrounding whitespace")
	}
	if err := requireHTTPSURL(latest.Dist.Tarball); err != nil {
		return Candidate{}, fmt.Errorf("npm candidate tarball: %w", err)
	}
	return Candidate{
		ComponentID: componentID,
		Version:     latest.Version,
		Source:      "npm registry",
		Provenance: "https://www.npmjs.com/package/" + packageName +
			"/v/" + latest.Version,
	}, nil
}

func supportedComponent(
	environment catalog.Environment,
	targetKind catalog.TargetKind,
	componentID string,
) (catalog.Component, catalog.TargetSupport, error) {
	for _, component := range environment.Catalog.Components {
		if component.ID != componentID {
			continue
		}
		support, exists := component.Targets[targetKind]
		if !exists || support.Status != catalog.StatusSupported {
			return catalog.Component{}, catalog.TargetSupport{}, fmt.Errorf(
				"component %q is not supported on %s",
				componentID,
				targetKind,
			)
		}
		return component, support, nil
	}
	return catalog.Component{}, catalog.TargetSupport{}, fmt.Errorf(
		"unknown component %q",
		componentID,
	)
}
