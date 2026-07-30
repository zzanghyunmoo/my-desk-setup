package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	hostadapter "github.com/zzanghyunmoo/my-desk-setup/internal/adapters/host"
	"github.com/zzanghyunmoo/my-desk-setup/internal/guest"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

const (
	testGuestCLIRevision     = "1.2.3 (commit=reviewed, date=2026-07-30T00:00:00Z)"
	testGuestCatalogRevision = "sha256:reviewed-catalog"
	testGuestImageURL        = "https://example.invalid/ubuntu-26.04.img"
	testGuestImageSHA        = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testGuestCreationNonce   = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func TestGuestRuntimeHandoffUsesExactRevisionAndBoundedArgv(t *testing.T) {
	tests := []struct {
		name       string
		action     planning.Action
		spec       guest.Spec
		inventory  transport.Result
		wantTarget string
		wantArgv   []string
	}{
		{
			name: "Lima",
			action: planning.Action{
				ID: "macos-host:local/lima", ComponentID: "lima",
			},
			spec: guest.Spec{WSLDistribution: "Ubuntu-26.04"},
			inventory: transport.Result{
				Stdout: `{"name":"mds","status":"Running","arch":"aarch64","limaVersion":"2.1.1"}`,
			},
			wantTarget: "lima-guest:mds",
			wantArgv: []string{
				"shell", "--tty=false", "mds", "--",
				"env",
				"MDS_IMAGE_CREATION_NONCE=" + testGuestCreationNonce,
				"MDS_IMAGE_PROVENANCE=" + testGuestImageURL,
				"MDS_IMAGE_REVISION=sha256:" + testGuestImageSHA,
				"/bin/sh", "-c", `exec "$HOME/.local/bin/mds" "$@"`,
				"mds", "plan", "--target", "lima-guest:mds",
				"--all", "--format", "json",
			},
		},
		{
			name: "WSL",
			action: planning.Action{
				ID: "windows-host:local/wsl", ComponentID: "wsl",
			},
			spec:       guest.Spec{WSLDistribution: "Ubuntu-26.04"},
			inventory:  transport.Result{Stdout: "Ubuntu-26.04\n"},
			wantTarget: "wsl-guest:Ubuntu-26.04",
			wantArgv: []string{
				"--distribution", "Ubuntu-26.04",
				"--exec", "env",
				"MDS_IMAGE_CREATION_NONCE=" + testGuestCreationNonce,
				"MDS_IMAGE_PROVENANCE=" + testGuestImageURL,
				"MDS_IMAGE_REVISION=sha256:" + testGuestImageSHA,
				"/bin/sh", "-c",
				`exec "$HOME/.local/bin/mds" "$@"`, "mds", "plan",
				"--target", "wsl-guest:Ubuntu-26.04",
				"--all", "--format", "json",
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			spec, ownershipRoot, marker := ownedGuestFixture(
				t,
				test.action,
				test.spec,
			)
			port := &guestRuntimePort{}
			port.result = func(command transport.Command) (transport.Result, error) {
				switch {
				case command.Executable == "limactl" &&
					slices.Equal(command.Arguments, []string{"list", "--json"}):
					return test.inventory, nil
				case command.Executable == "wsl.exe" &&
					len(command.Arguments) >= 2 &&
					slices.Equal(command.Arguments[:2], []string{"--list", "--quiet"}):
					return test.inventory, nil
				case command.Executable == "wsl.exe" &&
					slices.Equal(
						command.Arguments,
						[]string{"--list", "--running", "--quiet"},
					):
					return test.inventory, nil
				case isGuestImageIdentityReadCommand(command):
					return transport.Result{Stdout: marker}, nil
				case isGuestMDSCommand(command):
					return transport.Result{Stdout: guestPlanIdentityJSON(
						test.wantTarget,
						testGuestCLIRevision,
						testGuestCatalogRevision,
					)}, nil
				default:
					return transport.Result{}, nil
				}
			}
			runtime := hostadapter.GuestRuntime{
				Architecture:    "arm64",
				Port:            port,
				Delegate:        guestRuntimeDelegate{},
				Spec:            spec,
				CLIRevision:     testGuestCLIRevision,
				CatalogRevision: testGuestCatalogRevision,
				OwnershipRoot:   ownershipRoot,
			}

			observation, err := runtime.Observe(context.Background(), test.action)
			if err != nil {
				t.Fatalf("Observe(): %v", err)
			}
			if observation.State != adapters.StateReady {
				t.Fatalf("observation = %+v, want ready", observation)
			}
			imageIdentityReads := 0
			for _, command := range port.commands {
				if isGuestImageIdentityReadCommand(command) {
					imageIdentityReads++
				}
			}
			if imageIdentityReads != 1 {
				t.Fatalf(
					"guest image identity reads = %d, want one authoritative probe",
					imageIdentityReads,
				)
			}
			handoff := findGuestMDSCommand(t, port.commands)
			if !slices.Equal(handoff.Arguments, test.wantArgv) {
				t.Fatalf("handoff argv = %v, want %v", handoff.Arguments, test.wantArgv)
			}
			if handoff.Timeout <= 0 || handoff.OutputLimit <= 0 {
				t.Fatalf(
					"handoff bounds = timeout %s output %d, want explicit positive values",
					handoff.Timeout,
					handoff.OutputLimit,
				)
			}
			joined := handoff.Executable + " " + strings.Join(handoff.Arguments, " ")
			for _, forbidden := range []string{
				"curl ", "wget ", " cp ", " install ", " auth ", " login ",
			} {
				if strings.Contains(" "+joined+" ", forbidden) {
					t.Fatalf("handoff mutates guest or handles auth via %q: %s", forbidden, joined)
				}
			}
		})
	}
}

func TestGuestRuntimeHandoffCarriesAndVerifiesPinnedImageIdentity(t *testing.T) {
	const imageURL = "https://cloud-images.example/ubuntu-26.04-arm64.img"
	imageSHA := strings.Repeat("a", 64)
	action := planning.Action{
		ID: "macos-host:local/lima", ComponentID: "lima",
	}
	spec, ownershipRoot, marker := ownedGuestFixture(
		t,
		action,
		guest.Spec{Images: map[string]guest.ImageSpec{
			"arm64": {URL: imageURL, SHA256: imageSHA},
		}},
	)
	port := &guestRuntimePort{
		result: func(command transport.Command) (transport.Result, error) {
			switch {
			case command.Executable == "limactl" &&
				slices.Equal(command.Arguments, []string{"list", "--json"}):
				return transport.Result{
					Stdout: `{"name":"mds","status":"Running","arch":"aarch64","limaVersion":"2.1.1"}`,
				}, nil
			case isGuestImageIdentityReadCommand(command):
				return transport.Result{Stdout: marker}, nil
			case isGuestMDSCommand(command):
				return transport.Result{Stdout: guestPlanIdentityJSONWithImage(
					"lima-guest:mds",
					testGuestCLIRevision,
					testGuestCatalogRevision,
					"sha256:"+imageSHA,
					imageURL,
					testGuestCreationNonce,
				)}, nil
			default:
				return transport.Result{}, nil
			}
		},
	}
	runtime := hostadapter.GuestRuntime{
		Architecture:    "arm64",
		Port:            port,
		Delegate:        guestRuntimeDelegate{},
		Spec:            spec,
		CLIRevision:     testGuestCLIRevision,
		CatalogRevision: testGuestCatalogRevision,
		OwnershipRoot:   ownershipRoot,
	}

	observation, err := runtime.Observe(context.Background(), action)
	if err != nil {
		t.Fatalf("Observe(): %v", err)
	}
	if observation.State != adapters.StateReady {
		t.Fatalf("observation = %+v, want ready", observation)
	}
	handoff := findGuestMDSCommand(t, port.commands)
	joined := strings.Join(handoff.Arguments, " ")
	for _, expected := range []string{
		"MDS_IMAGE_CREATION_NONCE=" + testGuestCreationNonce,
		"MDS_IMAGE_REVISION=sha256:" + imageSHA,
		"MDS_IMAGE_PROVENANCE=" + imageURL,
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("handoff argv = %q, want %q", joined, expected)
		}
	}

	port.result = func(command transport.Command) (transport.Result, error) {
		switch {
		case command.Executable == "limactl" &&
			slices.Equal(command.Arguments, []string{"list", "--json"}):
			return transport.Result{
				Stdout: `{"name":"mds","status":"Running","arch":"aarch64","limaVersion":"2.1.1"}`,
			}, nil
		case isGuestImageIdentityReadCommand(command):
			return transport.Result{Stdout: marker}, nil
		case isGuestMDSCommand(command):
			return transport.Result{Stdout: guestPlanIdentityJSONWithImage(
				"lima-guest:mds",
				testGuestCLIRevision,
				testGuestCatalogRevision,
				"sha256:"+strings.Repeat("b", 64),
				imageURL,
				testGuestCreationNonce,
			)}, nil
		default:
			return transport.Result{}, nil
		}
	}
	observation, err = runtime.Observe(context.Background(), action)
	if err != nil {
		t.Fatalf("Observe(mismatch): %v", err)
	}
	if observation.State == adapters.StateReady ||
		!strings.Contains(observation.Detail, "image revision mismatch") {
		t.Fatalf("observation = %+v, want exact image mismatch", observation)
	}
}

func TestGuestRuntimeMissingOrStaleMDSWithoutReleaseMetadataRequiresAction(t *testing.T) {
	tests := []struct {
		name      string
		handoff   func() (transport.Result, error)
		wantCause string
	}{
		{
			name: "missing",
			handoff: func() (transport.Result, error) {
				return transport.Result{}, errors.New("mds executable not found")
			},
			wantCause: "missing",
		},
		{
			name: "stale CLI",
			handoff: func() (transport.Result, error) {
				return transport.Result{Stdout: guestPlanIdentityJSON(
					"lima-guest:mds",
					"1.2.2 (commit=stale, date=2026-07-29T00:00:00Z)",
					testGuestCatalogRevision,
				)}, nil
			},
			wantCause: "stale guest cli revision",
		},
		{
			name: "stale catalog",
			handoff: func() (transport.Result, error) {
				return transport.Result{Stdout: guestPlanIdentityJSON(
					"lima-guest:mds",
					testGuestCLIRevision,
					"sha256:stale-catalog",
				)}, nil
			},
			wantCause: "stale guest catalog revision",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			action := planning.Action{
				ID: "macos-host:local/lima", ComponentID: "lima",
			}
			spec, ownershipRoot, marker := ownedGuestFixture(
				t,
				action,
				guest.Spec{},
			)
			port := &guestRuntimePort{
				result: func(command transport.Command) (transport.Result, error) {
					switch {
					case command.Executable == "limactl" &&
						slices.Equal(command.Arguments, []string{"list", "--json"}):
						return transport.Result{
							Stdout: `{"name":"mds","status":"Running","arch":"aarch64","limaVersion":"2.1.1"}`,
						}, nil
					case isGuestImageIdentityReadCommand(command):
						return transport.Result{Stdout: marker}, nil
					case isGuestMDSCommand(command):
						return test.handoff()
					default:
						return transport.Result{}, nil
					}
				},
			}
			runtime := hostadapter.GuestRuntime{
				Architecture:    "arm64",
				Port:            port,
				Delegate:        guestRuntimeDelegate{},
				Spec:            spec,
				CLIRevision:     testGuestCLIRevision,
				CatalogRevision: testGuestCatalogRevision,
				OwnershipRoot:   ownershipRoot,
			}

			observation, err := runtime.Observe(context.Background(), action)
			if err != nil {
				t.Fatalf("Observe(): %v", err)
			}
			if observation.State == adapters.StateReady {
				t.Fatalf("observation = %+v, stale/missing guest mds must not be ready", observation)
			}
			if !strings.Contains(strings.ToLower(observation.Detail), test.wantCause) {
				t.Fatalf("observation detail = %q, want %q", observation.Detail, test.wantCause)
			}

			err = runtime.Apply(context.Background(), action)
			var actionRequired *adapters.ActionRequiredError
			if !errors.As(err, &actionRequired) {
				t.Fatalf("Apply() error = %v, want action-required bootstrap handoff", err)
			}
			for _, expected := range []string{
				"no reviewed Linux/arm64 artifact URL and SHA-256",
				testGuestCLIRevision,
				testGuestCatalogRevision,
			} {
				if !strings.Contains(actionRequired.Reason, expected) {
					t.Fatalf("action-required reason = %q, want %q", actionRequired.Reason, expected)
				}
			}
		})
	}
}

func TestGuestRuntimeAutomaticallyBootstrapsReviewedLinuxArtifact(t *testing.T) {
	const (
		artifactURL = "https://github.com/zzanghyunmoo/my-desk-setup/releases/download/v1.2.3/mds_1.2.3_linux_arm64.tar.gz"
		artifactSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)
	action := planning.Action{
		ID: "macos-host:local/lima", ComponentID: "lima",
	}
	spec, ownershipRoot, marker := ownedGuestFixture(t, action, guest.Spec{})
	handoffAttempts := 0
	port := &guestRuntimePort{
		result: func(command transport.Command) (transport.Result, error) {
			switch {
			case command.Executable == "limactl" &&
				slices.Equal(command.Arguments, []string{"list", "--json"}):
				return transport.Result{
					Stdout: `{"name":"mds","status":"Running","arch":"aarch64","limaVersion":"2.1.1"}`,
				}, nil
			case isGuestImageIdentityReadCommand(command):
				return transport.Result{Stdout: marker}, nil
			case isGuestBootstrapCommand(command):
				return transport.Result{}, nil
			case isGuestMDSCommand(command):
				handoffAttempts++
				if handoffAttempts < 2 {
					return transport.Result{}, errors.New("mds executable not found")
				}
				return transport.Result{Stdout: guestPlanIdentityJSON(
					"lima-guest:mds",
					testGuestCLIRevision,
					testGuestCatalogRevision,
				)}, nil
			default:
				return transport.Result{}, nil
			}
		},
	}
	runtime := hostadapter.GuestRuntime{
		Architecture:    "arm64",
		Port:            port,
		Delegate:        guestRuntimeDelegate{},
		Spec:            spec,
		CLIRevision:     testGuestCLIRevision,
		CatalogRevision: testGuestCatalogRevision,
		OwnershipRoot:   ownershipRoot,
		BootstrapArtifacts: map[string]hostadapter.GuestBootstrapArtifact{
			"arm64": {URL: artifactURL, SHA256: artifactSHA},
		},
	}
	if err := runtime.Apply(context.Background(), action); err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	var bootstrap transport.Command
	for _, command := range port.commands {
		if isGuestBootstrapCommand(command) {
			bootstrap = command
			break
		}
	}
	if len(bootstrap.Stdin) == 0 ||
		!strings.Contains(string(bootstrap.Stdin), "sha256sum -c") {
		t.Fatalf("bootstrap stdin does not contain checksum-verifying installer")
	}
	joined := strings.Join(bootstrap.Arguments, " ")
	for _, expected := range []string{artifactURL, artifactSHA, "/bin/sh -eu -s --"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("bootstrap argv = %q, want %q", joined, expected)
		}
	}
	for _, forbidden := range []string{" auth ", " login ", "token"} {
		if strings.Contains(strings.ToLower(" "+joined+" "), forbidden) {
			t.Fatalf("bootstrap argv contains forbidden auth surface %q", forbidden)
		}
	}
}

func TestGuestRuntimeBootstrapPreservesUserOwnedMDS(t *testing.T) {
	const artifactSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	action := planning.Action{
		ID: "macos-host:local/lima", ComponentID: "lima",
	}
	spec, ownershipRoot, marker := ownedGuestFixture(t, action, guest.Spec{})
	port := &guestRuntimePort{
		result: func(command transport.Command) (transport.Result, error) {
			switch {
			case command.Executable == "limactl" &&
				slices.Equal(command.Arguments, []string{"list", "--json"}):
				return transport.Result{
					Stdout: `{"name":"mds","status":"Running","arch":"aarch64","limaVersion":"2.1.1"}`,
				}, nil
			case isGuestImageIdentityReadCommand(command):
				return transport.Result{Stdout: marker}, nil
			case isGuestBootstrapCommand(command):
				return transport.Result{
					ExitCode: 73,
					Stderr:   "refusing to replace guest-local mds without the mds ownership marker",
				}, errors.New("bootstrap ownership conflict")
			case isGuestMDSCommand(command):
				return transport.Result{}, errors.New("stale guest-local mds")
			default:
				return transport.Result{}, nil
			}
		},
	}
	runtime := hostadapter.GuestRuntime{
		Architecture:    "arm64",
		Port:            port,
		Delegate:        guestRuntimeDelegate{},
		Spec:            spec,
		CLIRevision:     testGuestCLIRevision,
		CatalogRevision: testGuestCatalogRevision,
		OwnershipRoot:   ownershipRoot,
		BootstrapArtifacts: map[string]hostadapter.GuestBootstrapArtifact{
			"arm64": {
				URL: "https://example.invalid/mds.tar.gz", SHA256: artifactSHA,
			},
		},
	}
	err := runtime.Apply(context.Background(), action)
	var actionRequired *adapters.ActionRequiredError
	if !errors.As(err, &actionRequired) ||
		!strings.Contains(actionRequired.Reason, "ownership marker") {
		t.Fatalf("Apply() error = %v, want ownership action-required", err)
	}
}

type guestRuntimePort struct {
	commands []transport.Command
	result   func(transport.Command) (transport.Result, error)
}

func (port *guestRuntimePort) Run(
	_ context.Context,
	command transport.Command,
) (transport.Result, error) {
	port.commands = append(port.commands, command)
	if port.result != nil {
		return port.result(command)
	}
	return transport.Result{}, nil
}

type guestRuntimeDelegate struct{}

func (guestRuntimeDelegate) Observe(
	context.Context,
	planning.Action,
) (adapters.Observation, error) {
	return adapters.Observation{State: adapters.StateReady}, nil
}

func (guestRuntimeDelegate) Apply(context.Context, planning.Action) error {
	return nil
}

func (guestRuntimeDelegate) Verify(context.Context, planning.Action) error {
	return nil
}

func isGuestMDSCommand(command transport.Command) bool {
	for index := 0; index+1 < len(command.Arguments); index++ {
		if command.Arguments[index] == "mds" &&
			command.Arguments[index+1] == "plan" {
			return true
		}
	}
	return false
}

func isGuestBootstrapCommand(command transport.Command) bool {
	return len(command.Stdin) > 0 &&
		strings.Contains(string(command.Stdin), "mds.guest-bootstrap/v1")
}

func isGuestImageIdentityReadCommand(command transport.Command) bool {
	joined := strings.Join(command.Arguments, " ")
	return strings.Contains(joined, "/usr/bin/stat -c") &&
		strings.Contains(joined, "/etc/mds/image-identity-v1")
}

func ownedGuestFixture(
	t *testing.T,
	action planning.Action,
	spec guest.Spec,
) (guest.Spec, string, string) {
	t.Helper()
	if spec.WSLDistribution == "" {
		spec.WSLDistribution = "Ubuntu-26.04"
	}
	provider := action.ComponentID
	name := "mds"
	imageURL := testGuestImageURL
	imageSHA := testGuestImageSHA
	switch provider {
	case "lima":
		if spec.Images == nil {
			spec.Images = map[string]guest.ImageSpec{
				"arm64": {URL: imageURL, SHA256: imageSHA},
			}
		} else {
			imageURL = spec.Images["arm64"].URL
			imageSHA = spec.Images["arm64"].SHA256
		}
	case "wsl":
		name = spec.WSLDistribution
		if spec.WSLImages == nil {
			spec.WSLImages = map[string]guest.ImageSpec{
				"arm64": {URL: imageURL, SHA256: imageSHA},
			}
		} else {
			imageURL = spec.WSLImages["arm64"].URL
			imageSHA = spec.WSLImages["arm64"].SHA256
		}
	default:
		t.Fatalf("unsupported guest fixture provider %q", provider)
	}
	root := t.TempDir()
	if err := guest.PublishOwnership(root, guest.Ownership{
		Provider: provider, Name: name,
		ImageURL: imageURL, ImageSHA256: imageSHA,
		CreationNonce: testGuestCreationNonce,
	}); err != nil {
		t.Fatalf("PublishOwnership(): %v", err)
	}
	record, exists, err := guest.LoadOwnership(root, provider, name)
	if err != nil || !exists {
		t.Fatalf(
			"LoadOwnership() record=%+v exists=%t error=%v",
			record,
			exists,
			err,
		)
	}
	marker := "schema=mds.guest-image/v2\n" +
		"image_revision=sha256:" + imageSHA + "\n" +
		"image_provenance=" + imageURL + "\n" +
		"creation_nonce=" + record.CreationNonce + "\n"
	return spec, root, marker
}

func findGuestMDSCommand(
	t *testing.T,
	commands []transport.Command,
) transport.Command {
	t.Helper()
	for _, command := range commands {
		if isGuestMDSCommand(command) {
			return command
		}
	}
	t.Fatal("guest-local mds handoff command was not executed")
	return transport.Command{}
}

func guestPlanIdentityJSON(
	targetID,
	cliRevision,
	catalogRevision string,
) string {
	return guestPlanIdentityJSONWithImage(
		targetID,
		cliRevision,
		catalogRevision,
		"sha256:"+testGuestImageSHA,
		testGuestImageURL,
		testGuestCreationNonce,
	)
}

func guestPlanIdentityJSONWithImage(
	targetID,
	cliRevision,
	catalogRevision,
	imageRevision,
	imageProvenance,
	imageCreationNonce string,
) string {
	id, _ := target.ParseID(targetID)
	encoded, _ := json.Marshal(struct {
		CatalogRevision string       `json:"catalog_revision"`
		Target          target.Facts `json:"target"`
	}{
		CatalogRevision: catalogRevision,
		Target: target.Facts{
			ID: id, CLIRevision: cliRevision, CatalogRevision: catalogRevision,
			ImageRevision: imageRevision, ImageProvenance: imageProvenance,
			ImageCreationNonce: imageCreationNonce,
		},
	})
	return string(encoded)
}
