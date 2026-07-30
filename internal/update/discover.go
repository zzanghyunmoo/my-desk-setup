package update

import (
	"bytes"
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

const (
	discoveryTimeout = 30 * time.Second
	maxMetadataBytes = 1 << 20
)

var errCrossOriginRedirect = errors.New(
	"cross-origin redirect is forbidden",
)

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
		return Candidate{}, invalid(err)
	}
	if component.VersionPolicy.Mode != "pinned" {
		return Candidate{}, invalid(fmt.Errorf(
			"component %q is %s-owned; no exact candidate discovery is available",
			componentID,
			component.VersionPolicy.Mode,
		))
	}
	if support.Installer != "bun" {
		return Candidate{}, invalid(fmt.Errorf(
			"component %q requires a reviewed candidate file with exact provenance and artifacts",
			componentID,
		))
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
		return Candidate{}, invalid(errors.New("npm package name is required"))
	}
	if registryBase == "" {
		registryBase = defaultNPMRegistry
	}
	if err := validateRegistryBase(registryBase); err != nil {
		return Candidate{}, invalid(err)
	}
	client = boundedHTTPClient(client)
	endpoint := strings.TrimRight(registryBase, "/") + "/" +
		url.PathEscape(packageName) + "/latest"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Candidate{}, invalid(errors.New("create npm latest request"))
	}
	response, err := client.Do(request)
	if err != nil {
		return Candidate{}, redactedRequestError(
			"discover npm candidate",
			err,
		)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			resultErr = errors.Join(
				resultErr,
				errors.New("close npm candidate response body failed"),
			)
		}
	}()
	if err := requireResponseOrigin(response, request.URL); err != nil {
		return Candidate{}, unreachable(err)
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return Candidate{}, unreachable(errors.New(
			"npm candidate discovery was rate limited",
		))
	}
	if response.StatusCode != http.StatusOK {
		return Candidate{}, unreachable(fmt.Errorf(
			"discover npm candidate: HTTP %s",
			response.Status,
		))
	}
	content, err := io.ReadAll(io.LimitReader(
		response.Body,
		maxMetadataBytes+1,
	))
	if err != nil {
		return Candidate{}, unreachable(errors.New(
			"read npm candidate metadata failed",
		))
	}
	if len(content) > maxMetadataBytes {
		return Candidate{}, unreachable(errors.New(
			"npm candidate metadata exceeds 1 MiB",
		))
	}
	var latest npmLatest
	decoder := json.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&latest); err != nil {
		return Candidate{}, unreachable(errors.New(
			"decode npm candidate metadata failed",
		))
	}
	if latest.Version == "" || latest.Dist.Integrity == "" || latest.Dist.Tarball == "" {
		return Candidate{}, unreachable(errors.New(
			"npm candidate is missing version, integrity, or tarball provenance",
		))
	}
	if latest.Version != strings.TrimSpace(latest.Version) {
		return Candidate{}, unreachable(errors.New(
			"npm candidate version contains surrounding whitespace",
		))
	}
	if _, err := exactartifact.DecodeSHA512SRI(latest.Dist.Integrity); err != nil {
		return Candidate{}, unreachable(fmt.Errorf("npm candidate SRI: %w", err))
	}
	canonicalTarball, err := catalog.CanonicalNPMTarballURL(
		registryBase,
		packageName,
		latest.Version,
	)
	if err != nil {
		return Candidate{}, invalid(fmt.Errorf("canonical npm tarball: %w", err))
	}
	if latest.Dist.Tarball != canonicalTarball {
		return Candidate{}, unreachable(errors.New(
			"npm candidate tarball is not canonical",
		))
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
		return "", invalid(errors.New("create npm tarball request"))
	}
	response, err := client.Do(request)
	if err != nil {
		return "", redactedRequestError(
			"download npm candidate tarball",
			err,
		)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			resultErr = errors.Join(
				resultErr,
				errors.New("close npm candidate tarball response body failed"),
			)
		}
	}()
	if err := requireResponseOrigin(response, request.URL); err != nil {
		return "", unreachable(err)
	}
	if response.StatusCode != http.StatusOK {
		return "", unreachable(fmt.Errorf(
			"download npm candidate tarball: HTTP %s",
			response.Status,
		))
	}
	if response.Request == nil ||
		response.Request.URL.String() != tarballURL {
		return "", unreachable(errors.New(
			"npm candidate tarball redirected away from its canonical URL",
		))
	}
	verifiedDigest, err := exactartifact.CopyAndVerify(
		response.Body,
		io.Discard,
		"",
		integrity,
		exactartifact.MaxDownloadBytes,
	)
	if err != nil {
		return "", unreachable(fmt.Errorf("verify npm candidate tarball: %w", err))
	}
	return verifiedDigest, nil
}

func validateRegistryBase(value string) error {
	parsed, err := url.ParseRequestURI(strings.TrimRight(value, "/"))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New(
			"npm registry must be an absolute credential-free HTTPS URL",
		)
	}
	return nil
}

func boundedHTTPClient(input *http.Client) *http.Client {
	client := &http.Client{}
	if input != nil {
		*client = *input
	}
	if client.Timeout <= 0 || client.Timeout > discoveryTimeout {
		client.Timeout = discoveryTimeout
	}
	previousRedirect := client.CheckRedirect
	client.CheckRedirect = func(
		request *http.Request,
		via []*http.Request,
	) error {
		if len(via) == 0 {
			return errors.New("redirect origin is unavailable")
		}
		if request.URL.User != nil ||
			!sameOrigin(via[0].URL, request.URL) {
			return errCrossOriginRedirect
		}
		if previousRedirect != nil {
			return previousRedirect(request, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return client
}

func requireResponseOrigin(
	response *http.Response,
	requested *url.URL,
) error {
	if response.Request == nil || response.Request.URL == nil ||
		response.Request.URL.User != nil ||
		!sameOrigin(requested, response.Request.URL) {
		return errCrossOriginRedirect
	}
	return nil
}

func sameOrigin(left, right *url.URL) bool {
	if left == nil || right == nil {
		return false
	}
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Hostname(), right.Hostname()) &&
		effectivePort(left) == effectivePort(right)
}

func effectivePort(value *url.URL) string {
	if port := value.Port(); port != "" {
		return port
	}
	switch strings.ToLower(value.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	default:
		return ""
	}
}

func redactedRequestError(operation string, err error) error {
	if errors.Is(err, errCrossOriginRedirect) {
		return unreachable(fmt.Errorf("%s: %w", operation, errCrossOriginRedirect))
	}
	return unreachable(errors.New(operation + ": request failed"))
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
