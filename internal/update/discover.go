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

	exactartifact "github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
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
) (candidate Candidate, resultErr error) {
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
	defer func() {
		if err := response.Body.Close(); err != nil {
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("close npm candidate response body: %w", err),
			)
		}
	}()
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
	if _, err := exactartifact.DecodeSHA512SRI(latest.Dist.Integrity); err != nil {
		return Candidate{}, fmt.Errorf("npm candidate SRI: %w", err)
	}
	canonicalTarball, err := catalog.CanonicalNPMTarballURL(
		registryBase,
		packageName,
		latest.Version,
	)
	if err != nil {
		return Candidate{}, fmt.Errorf("canonical npm tarball: %w", err)
	}
	if latest.Dist.Tarball != canonicalTarball {
		return Candidate{}, fmt.Errorf(
			"npm candidate tarball is not canonical: expected %s got %s",
			canonicalTarball,
			latest.Dist.Tarball,
		)
	}
	digest, err := fetchNPMTarball(
		ctx,
		client,
		canonicalTarball,
		latest.Dist.Integrity,
	)
	if err != nil {
		return Candidate{}, err
	}
	return Candidate{
		ComponentID: componentID,
		Version:     latest.Version,
		Source:      "npm registry",
		Provenance: "https://www.npmjs.com/package/" + packageName +
			"/v/" + latest.Version,
		NPM: &catalog.NPMArtifact{
			Tarball: canonicalTarball, Integrity: latest.Dist.Integrity,
			SHA256: digest,
		},
	}, nil
}

func fetchNPMTarball(
	ctx context.Context,
	client *http.Client,
	tarballURL,
	integrity string,
) (digest string, resultErr error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		tarballURL,
		nil,
	)
	if err != nil {
		return "", fmt.Errorf("create npm tarball request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("download npm candidate tarball: %w", err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("close npm candidate tarball response body: %w", err),
			)
		}
	}()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf(
			"download npm candidate tarball: HTTP %s",
			response.Status,
		)
	}
	if response.Request == nil ||
		response.Request.URL.String() != tarballURL {
		return "", errors.New("npm candidate tarball redirected away from its canonical URL")
	}
	verifiedDigest, err := exactartifact.CopyAndVerify(
		response.Body,
		io.Discard,
		"",
		integrity,
		exactartifact.MaxDownloadBytes,
	)
	if err != nil {
		return "", fmt.Errorf("verify npm candidate tarball: %w", err)
	}
	return verifiedDigest, nil
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
