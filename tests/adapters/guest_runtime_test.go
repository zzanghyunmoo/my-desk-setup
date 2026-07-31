package adapters_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	hostadapter "github.com/zzanghyunmoo/my-desk-setup/internal/adapters/host"
	"github.com/zzanghyunmoo/my-desk-setup/internal/guest"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func TestLimaRuntimeCreatesOnePinnedImageTemplateFromStdin(t *testing.T) {
	for _, test := range []struct {
		architecture string
		limaArch     string
	}{
		{architecture: "arm64", limaArch: "aarch64"},
		{architecture: "amd64", limaArch: "x86_64"},
	} {
		test := test
		t.Run(test.architecture, func(t *testing.T) {
			const imageURL = "https://cloud-images.example/ubuntu-26.04.img"
			imageSHA := strings.Repeat("a", 64)
			ownershipRoot := t.TempDir()
			port := &recordingPort{
				result: func(command transport.Command) transport.Result {
					if command.Executable == "limactl" &&
						len(command.Arguments) > 0 &&
						command.Arguments[0] == "list" {
						return transport.Result{}
					}
					if strings.Contains(strings.Join(command.Arguments, " "), "mds plan") {
						return transport.Result{Stdout: hostRuntimePlanIdentityWithRevisions(
							"lima-guest:mds",
							"cli",
							"sha256:catalog",
							imageURL,
							imageSHA,
							ownershipNonce(
								t,
								ownershipRoot,
								"lima",
								"mds",
							),
						)}
					}
					if isGuestImageIdentityReadCommand(command) {
						return transport.Result{Stdout: guestImageIdentityMarkerFromOwnership(
							t,
							ownershipRoot,
							"lima",
							"mds",
							imageURL,
							imageSHA,
						)}
					}
					return transport.Result{}
				},
			}
			runtime := hostadapter.GuestRuntime{
				Architecture: test.architecture,
				Port:         port,
				Delegate:     readyComponent{},
				Spec: guest.Spec{Images: map[string]guest.ImageSpec{
					test.architecture: {URL: imageURL, SHA256: imageSHA},
				}},
				CLIRevision:     "cli",
				CatalogRevision: "sha256:catalog",
				OwnershipRoot:   ownershipRoot,
			}

			err := runtime.Apply(context.Background(), planning.Action{
				ID: "macos-host:local/lima", ComponentID: "lima",
			})
			if err != nil {
				t.Fatalf("Apply(): %v", err)
			}
			var create transport.Command
			for _, command := range port.commands {
				if command.Executable == "limactl" &&
					len(command.Arguments) > 0 &&
					command.Arguments[0] == "create" {
					create = command
					break
				}
			}
			if !reflect.DeepEqual(
				create.Arguments,
				[]string{"create", "--name", "mds", "-"},
			) {
				t.Fatalf("create arguments = %v, want stdin template", create.Arguments)
			}
			template := string(create.Stdin)
			for _, expected := range []string{
				"arch: " + test.limaArch,
				"location: " + imageURL,
				"arch: " + test.limaArch,
				"digest: sha256:" + imageSHA,
			} {
				if !strings.Contains(template, expected) {
					t.Fatalf("template does not contain %q:\n%s", expected, template)
				}
			}
			if strings.Count(template, "location:") != 1 ||
				strings.Contains(template, ".images[0]") ||
				strings.Contains(strings.Join(create.Arguments, " "), "--set") {
				t.Fatalf("template is not an exact single-image template: %+v\n%s", create, template)
			}
		})
	}
}

func TestGuestRuntimeRefusesPreExistingGuestWithoutOwnership(t *testing.T) {
	for _, test := range []struct {
		name      string
		action    planning.Action
		spec      guest.Spec
		inventory transport.Result
	}{
		{
			name:   "Lima",
			action: planning.Action{ID: "macos-host:local/lima", ComponentID: "lima"},
			spec: guest.Spec{Images: map[string]guest.ImageSpec{
				"arm64": {
					URL: "https://example.invalid/lima.img", SHA256: strings.Repeat("a", 64),
				},
			}},
			inventory: transport.Result{
				Stdout: `{"name":"mds","status":"Running","arch":"aarch64","limaVersion":"2.1.1"}`,
			},
		},
		{
			name:   "WSL",
			action: planning.Action{ID: "windows-host:local/wsl", ComponentID: "wsl"},
			spec: guest.Spec{
				WSLDistribution: "Ubuntu-26.04",
				WSLImages: map[string]guest.ImageSpec{
					"arm64": {
						URL: "https://example.invalid/ubuntu.wsl", SHA256: strings.Repeat("b", 64),
					},
				},
			},
			inventory: transport.Result{Stdout: "Ubuntu-26.04\n"},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			port := &recordingPort{
				result: func(command transport.Command) transport.Result {
					if command.Executable == "limactl" &&
						reflect.DeepEqual(command.Arguments, []string{"list", "--json"}) {
						return test.inventory
					}
					if command.Executable == "wsl.exe" &&
						reflect.DeepEqual(command.Arguments, []string{"--list", "--quiet"}) {
						return test.inventory
					}
					return transport.Result{}
				},
			}
			runtime := hostadapter.GuestRuntime{
				Architecture:    "arm64",
				Port:            port,
				Delegate:        readyComponent{},
				Spec:            test.spec,
				CLIRevision:     "cli",
				CatalogRevision: "sha256:catalog",
				OwnershipRoot:   t.TempDir(),
			}

			observation, err := runtime.Observe(context.Background(), test.action)
			if err != nil {
				t.Fatalf("Observe(): %v", err)
			}
			if observation.State != adapters.StateConflict ||
				!strings.Contains(observation.Detail, "not owned by mds") {
				t.Fatalf("observation = %+v, want ownership conflict", observation)
			}
			err = runtime.Apply(context.Background(), test.action)
			var actionRequired *adapters.ActionRequiredError
			if !errors.As(err, &actionRequired) ||
				!strings.Contains(actionRequired.Reason, "not owned by mds") {
				t.Fatalf("Apply() error = %v, want ownership action-required", err)
			}
			if len(port.commands) != 2 {
				t.Fatalf("commands = %+v, want inventory reads only", port.commands)
			}
		})
	}
}

func TestGuestRuntimeRefusesSameNameReplacementWithStaleOwnership(t *testing.T) {
	for _, test := range []struct {
		name      string
		action    planning.Action
		spec      guest.Spec
		provider  string
		guestName string
		inventory string
	}{
		{
			name: "Lima",
			action: planning.Action{
				ID: "macos-host:local/lima", ComponentID: "lima",
			},
			spec: guest.Spec{Images: map[string]guest.ImageSpec{
				"arm64": {
					URL:    "https://example.invalid/lima.img",
					SHA256: strings.Repeat("a", 64),
				},
			}},
			provider:  "lima",
			guestName: "mds",
			inventory: `{"name":"mds","status":"Running","arch":"aarch64","limaVersion":"2.1.1"}`,
		},
		{
			name: "WSL",
			action: planning.Action{
				ID: "windows-host:local/wsl", ComponentID: "wsl",
			},
			spec: guest.Spec{
				WSLDistribution: "Ubuntu-26.04",
				WSLImages: map[string]guest.ImageSpec{
					"arm64": {
						URL:    "https://example.invalid/ubuntu.wsl",
						SHA256: strings.Repeat("a", 64),
					},
				},
			},
			provider:  "wsl",
			guestName: "Ubuntu-26.04",
			inventory: "Ubuntu-26.04\n",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			ownershipRoot := t.TempDir()
			image := test.spec.Images["arm64"]
			if test.provider == "wsl" {
				image = test.spec.WSLImages["arm64"]
			}
			if err := guest.PublishOwnership(
				ownershipRoot,
				guest.Ownership{
					Provider: test.provider, Name: test.guestName,
					ImageURL: image.URL, ImageSHA256: image.SHA256,
					CreationNonce: strings.Repeat("b", 64),
				},
			); err != nil {
				t.Fatalf("PublishOwnership(): %v", err)
			}
			port := &recordingPort{
				err: func(command transport.Command) error {
					if isGuestImageIdentityReadCommand(command) {
						return errors.New(
							"guest creation identity does not match",
						)
					}
					return nil
				},
				result: func(command transport.Command) transport.Result {
					if command.Executable == "limactl" &&
						reflect.DeepEqual(
							command.Arguments,
							[]string{"list", "--json"},
						) {
						return transport.Result{Stdout: test.inventory}
					}
					if command.Executable == "wsl.exe" &&
						len(command.Arguments) > 0 &&
						command.Arguments[0] == "--list" {
						return transport.Result{Stdout: test.inventory}
					}
					return transport.Result{}
				},
			}
			runtime := hostadapter.GuestRuntime{
				Architecture: "arm64", Port: port,
				Delegate: readyComponent{}, Spec: test.spec,
				CLIRevision: "cli", CatalogRevision: "sha256:catalog",
				OwnershipRoot: ownershipRoot,
			}

			observation, err := runtime.Observe(
				context.Background(),
				test.action,
			)
			if err != nil {
				t.Fatalf("Observe(): %v", err)
			}
			if observation.State != adapters.StateConflict ||
				!strings.Contains(
					observation.Detail,
					"creation identity does not match",
				) {
				t.Fatalf(
					"observation = %+v, want replacement conflict",
					observation,
				)
			}
			err = runtime.Apply(context.Background(), test.action)
			var actionRequired *adapters.ActionRequiredError
			if !errors.As(err, &actionRequired) ||
				!strings.Contains(
					actionRequired.Reason,
					"creation identity does not match",
				) {
				t.Fatalf(
					"Apply() error = %v, want replacement action-required",
					err,
				)
			}
			joined := recordedArgv(port.commands)
			for _, forbidden := range []string{
				"limactl start",
				"limactl create",
				"mds.guest-bootstrap/v1",
				" mds plan ",
			} {
				if strings.Contains(joined, forbidden) {
					t.Fatalf(
						"replacement guest received mutation %q:\n%s",
						forbidden,
						joined,
					)
				}
			}
		})
	}
}

func TestGuestRuntimeDoesNotAdoptLateProviderGuestFromPreparingIntent(t *testing.T) {
	const imageURL = "https://example.invalid/lima.img"
	imageSHA := strings.Repeat("a", 64)
	created := false
	failCreate := true
	ownershipRoot := t.TempDir()
	port := &recordingPort{
		result: func(command transport.Command) transport.Result {
			if command.Executable == "limactl" &&
				reflect.DeepEqual(command.Arguments, []string{"list", "--json"}) {
				if created {
					return transport.Result{
						Stdout: `{"name":"mds","status":"Running","arch":"aarch64","limaVersion":"2.1.1"}`,
					}
				}
				return transport.Result{}
			}
			if strings.Contains(strings.Join(command.Arguments, " "), "mds plan") {
				return transport.Result{Stdout: hostRuntimePlanIdentity(
					"lima-guest:mds",
					imageURL,
					imageSHA,
					ownershipNonce(
						t,
						ownershipRoot,
						"lima",
						"mds",
					),
				)}
			}
			return transport.Result{}
		},
		err: func(command transport.Command) error {
			if command.Executable == "limactl" &&
				len(command.Arguments) > 0 &&
				command.Arguments[0] == "create" &&
				failCreate {
				created = true
				failCreate = false
				return errors.New("provider returned after late creation")
			}
			return nil
		},
	}
	runtime := hostadapter.GuestRuntime{
		Architecture: "arm64",
		Port:         port,
		Delegate:     readyComponent{},
		Spec: guest.Spec{Images: map[string]guest.ImageSpec{
			"arm64": {URL: imageURL, SHA256: imageSHA},
		}},
		CLIRevision:     hostRuntimeCLIRevision,
		CatalogRevision: hostRuntimeCatalogRevision,
		OwnershipRoot:   ownershipRoot,
	}
	action := planning.Action{
		ID: "macos-host:local/lima", ComponentID: "lima",
	}

	if err := runtime.Apply(context.Background(), action); err == nil {
		t.Fatal("Apply(first) succeeded, want simulated provider error")
	}
	record, exists, err := guest.LoadOwnership(
		ownershipRoot,
		"lima",
		"mds",
	)
	if err != nil || !exists || record.Phase != guest.OwnershipPreparing {
		t.Fatalf(
			"ownership intent after provider error record=%+v exists=%t err=%v",
			record,
			exists,
			err,
		)
	}
	err = runtime.Apply(context.Background(), action)
	var actionRequired *adapters.ActionRequiredError
	if !errors.As(err, &actionRequired) ||
		!strings.Contains(actionRequired.Reason, "uncommitted") {
		t.Fatalf("Apply(resume) error = %v, want ownership conflict", err)
	}
	if got := strings.Count(recordedArgv(port.commands), "limactl create --name mds -"); got != 1 {
		t.Fatalf("create command count = %d, want one late successful mutation", got)
	}
}

func TestWSLRuntimeRequiresConfiguredNonRootDefaultUser(t *testing.T) {
	const (
		imageURL = "https://example.invalid/ubuntu.wsl"
	)
	imageSHA := strings.Repeat("a", 64)
	ownershipRoot := t.TempDir()
	if err := guest.PublishOwnership(
		ownershipRoot,
		guest.Ownership{
			Provider: "wsl", Name: "Ubuntu-26.04",
			ImageURL: imageURL, ImageSHA256: imageSHA,
			CreationNonce: strings.Repeat("b", 64),
		},
	); err != nil {
		t.Fatalf("PublishOwnership(): %v", err)
	}
	port := &recordingPort{
		result: func(command transport.Command) transport.Result {
			if command.Executable == "wsl.exe" &&
				len(command.Arguments) > 0 &&
				command.Arguments[0] == "--list" {
				return transport.Result{Stdout: "Ubuntu-26.04\n"}
			}
			if isGuestImageIdentityReadCommand(command) {
				return transport.Result{
					Stdout: guestImageIdentityMarkerFromOwnership(
						t,
						ownershipRoot,
						"wsl",
						"Ubuntu-26.04",
						imageURL,
						imageSHA,
					),
				}
			}
			return transport.Result{}
		},
		err: func(command transport.Command) error {
			if strings.Contains(
				strings.Join(command.Arguments, " "),
				"mds-default-user",
			) {
				return errors.New("default WSL user is root")
			}
			return nil
		},
	}
	runtime := hostadapter.GuestRuntime{
		Architecture: "arm64", Port: port,
		Delegate: readyComponent{}, OwnershipRoot: ownershipRoot,
		Spec: guest.Spec{
			WSLDistribution: "Ubuntu-26.04",
			WSLImages: map[string]guest.ImageSpec{
				"arm64": {URL: imageURL, SHA256: imageSHA},
			},
		},
		CLIRevision: "cli", CatalogRevision: "sha256:catalog",
	}

	err := runtime.Apply(context.Background(), planning.Action{
		ID: "windows-host:local/wsl", ComponentID: "wsl",
	})
	var actionRequired *adapters.ActionRequiredError
	if !errors.As(err, &actionRequired) ||
		!strings.Contains(actionRequired.Reason, "create the Linux user") {
		t.Fatalf("Apply() error = %v, want non-root user action-required", err)
	}
	if strings.Contains(recordedArgv(port.commands), "mds.guest-bootstrap/v1") {
		t.Fatalf(
			"root-default WSL reached guest bootstrap:\n%s",
			recordedArgv(port.commands),
		)
	}
}

func TestGuestRuntimeDoesNotAdoptExternalGuestAfterFailedCreation(t *testing.T) {
	const imageURL = "https://example.invalid/lima.img"
	imageSHA := strings.Repeat("a", 64)
	externalGuest := false
	createAttempts := 0
	port := &recordingPort{
		result: func(command transport.Command) transport.Result {
			if command.Executable == "limactl" &&
				reflect.DeepEqual(command.Arguments, []string{"list", "--json"}) {
				if externalGuest {
					return transport.Result{
						Stdout: `{"name":"mds","status":"Running","arch":"aarch64","limaVersion":"2.1.1"}`,
					}
				}
			}
			return transport.Result{}
		},
		err: func(command transport.Command) error {
			if command.Executable == "limactl" &&
				len(command.Arguments) > 0 &&
				command.Arguments[0] == "create" {
				createAttempts++
				return errors.New("provider failed before creating a guest")
			}
			return nil
		},
	}
	runtime := hostadapter.GuestRuntime{
		Architecture: "arm64",
		Port:         port,
		Delegate:     readyComponent{},
		Spec: guest.Spec{Images: map[string]guest.ImageSpec{
			"arm64": {URL: imageURL, SHA256: imageSHA},
		}},
		CLIRevision:     "cli",
		CatalogRevision: "sha256:catalog",
		OwnershipRoot:   t.TempDir(),
	}
	action := planning.Action{
		ID: "macos-host:local/lima", ComponentID: "lima",
	}
	if err := runtime.Apply(context.Background(), action); err == nil {
		t.Fatal("Apply(first) succeeded, want provider failure")
	}
	externalGuest = true
	err := runtime.Apply(context.Background(), action)
	var actionRequired *adapters.ActionRequiredError
	if !errors.As(err, &actionRequired) ||
		!strings.Contains(actionRequired.Reason, "uncommitted") {
		t.Fatalf("Apply(external guest) error = %v, want ownership conflict", err)
	}
	if createAttempts != 1 {
		t.Fatalf("create attempts = %d, want no retry against external guest", createAttempts)
	}
}

func TestGuestRuntimeRejectsOwnedGuestWithDifferentImageIdentity(t *testing.T) {
	const imageURL = "https://example.invalid/current.img"
	imageSHA := strings.Repeat("a", 64)
	ownershipRoot := t.TempDir()
	if err := guest.PublishOwnership(ownershipRoot, guest.Ownership{
		Provider:    "lima",
		Name:        "mds",
		ImageURL:    "https://example.invalid/previous.img",
		ImageSHA256: strings.Repeat("b", 64),
	}); err != nil {
		t.Fatalf("PublishOwnership(): %v", err)
	}
	port := &recordingPort{result: func(command transport.Command) transport.Result {
		if command.Executable == "limactl" {
			return transport.Result{
				Stdout: `{"name":"mds","status":"Running","arch":"aarch64","limaVersion":"2.1.1"}`,
			}
		}
		return transport.Result{}
	}}
	runtime := hostadapter.GuestRuntime{
		Architecture: "arm64",
		Port:         port,
		Delegate:     readyComponent{},
		Spec: guest.Spec{Images: map[string]guest.ImageSpec{
			"arm64": {URL: imageURL, SHA256: imageSHA},
		}},
		CLIRevision:     "cli",
		CatalogRevision: "sha256:catalog",
		OwnershipRoot:   ownershipRoot,
	}
	action := planning.Action{
		ID: "macos-host:local/lima", ComponentID: "lima",
	}

	observation, err := runtime.Observe(context.Background(), action)
	if err != nil {
		t.Fatalf("Observe(): %v", err)
	}
	if observation.State != adapters.StateConflict ||
		!strings.Contains(observation.Detail, "image provenance conflicts") {
		t.Fatalf("observation = %+v, want image ownership conflict", observation)
	}
	err = runtime.Apply(context.Background(), action)
	var actionRequired *adapters.ActionRequiredError
	if !errors.As(err, &actionRequired) ||
		!strings.Contains(actionRequired.Reason, "image provenance conflicts") {
		t.Fatalf("Apply() error = %v, want image ownership action-required", err)
	}
	if strings.Contains(recordedArgv(port.commands), " start ") ||
		strings.Contains(recordedArgv(port.commands), " create ") {
		t.Fatalf("mutation executed after image conflict: %+v", port.commands)
	}
}

type recordingPort struct {
	commands []transport.Command
	result   func(transport.Command) transport.Result
	err      func(transport.Command) error
}

func (port *recordingPort) Run(
	_ context.Context,
	command transport.Command,
) (transport.Result, error) {
	port.commands = append(port.commands, command)
	if port.err != nil {
		if err := port.err(command); err != nil {
			return transport.Result{}, err
		}
	}
	if port.result != nil {
		return port.result(command), nil
	}
	return transport.Result{}, nil
}

type readyComponent struct {
	observation adapters.Observation
}

func (component readyComponent) Observe(
	context.Context,
	planning.Action,
) (adapters.Observation, error) {
	if component.observation.State == "" {
		return adapters.Observation{State: adapters.StateReady}, nil
	}
	return component.observation, nil
}

func (readyComponent) Apply(context.Context, planning.Action) error { return nil }
func (readyComponent) Verify(context.Context, planning.Action) error {
	return nil
}

func recordedArgv(commands []transport.Command) string {
	var lines []string
	for _, command := range commands {
		lines = append(lines, strings.Join(append(
			[]string{command.Executable},
			command.Arguments...,
		), " "))
	}
	return strings.Join(lines, "\n")
}

func isGuestImageIdentityReadCommand(command transport.Command) bool {
	joined := strings.Join(command.Arguments, " ")
	return strings.Contains(joined, "/usr/bin/stat -c") &&
		strings.Contains(joined, "/etc/mds/image-identity-v1")
}

func guestImageIdentityMarkerFromOwnership(
	t *testing.T,
	root,
	provider,
	name,
	imageURL,
	imageSHA string,
) string {
	t.Helper()
	record, exists, err := guest.LoadOwnership(root, provider, name)
	if err != nil || !exists {
		t.Fatalf(
			"LoadOwnership() record=%+v exists=%t error=%v",
			record,
			exists,
			err,
		)
	}
	return "schema=mds.guest-image/v2\n" +
		"image_revision=sha256:" + imageSHA + "\n" +
		"image_provenance=" + imageURL + "\n"
}
