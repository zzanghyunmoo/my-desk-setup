package adapters_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	catalogdata "github.com/zzanghyunmoo/my-desk-setup/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	hostadapter "github.com/zzanghyunmoo/my-desk-setup/internal/adapters/host"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/guest"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
	"github.com/zzanghyunmoo/my-desk-setup/internal/version"
)

const (
	hostRuntimeCLIRevision     = "1.2.3 (commit=reviewed, date=2026-07-30T00:00:00Z)"
	hostRuntimeCatalogRevision = "sha256:reviewed-catalog"
)

func TestHostAllContainsNoGuestToolchainOrAuthCommand(t *testing.T) {
	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		t.Fatalf("LoadFS(): %v", err)
	}
	id, _ := target.NewID(target.KindMacOSHost, "local")
	plan, err := planning.Build(
		environment,
		target.Facts{ID: id, OS: "darwin", Architecture: "arm64", Reachable: true},
		planning.All(),
	)
	if err != nil {
		t.Fatalf("Build(): %v", err)
	}
	for _, action := range plan.Actions {
		switch action.ComponentID {
		case "java", "kotlin", "go", "python", "flutter", "neovim", "docker-engine":
			t.Fatalf("host all contains guest-owned component %q", action.ComponentID)
		}
		for _, command := range action.Verification {
			for _, argument := range command {
				if argument == "login" || argument == "auth" {
					t.Fatalf("verification contains authentication command: %v", command)
				}
			}
		}
		if action.ComponentID == "lima" {
			for _, key := range []string{
				"guest_distribution", "image_kind", "image_sha256", "image_url",
			} {
				if action.Inputs[key] == "" {
					t.Fatalf("Lima plan action is missing reviewed input %q: %+v", key, action)
				}
			}
		}
	}
}

func TestLimaRuntimeCreatesPinnedUbuntuGuest(t *testing.T) {
	imageURL := "https://cloud-images.example/ubuntu-26.04-arm64.img"
	imageSHA256 := strings.Repeat("a", 64)
	ownershipRoot := t.TempDir()
	port := &recordingPort{
		result: func(command transport.Command) transport.Result {
			if command.Executable == "limactl" &&
				len(command.Arguments) > 0 &&
				command.Arguments[0] == "list" {
				return transport.Result{Stdout: ""}
			}
			if isHostRuntimeMDSCommand(command) {
				return transport.Result{Stdout: hostRuntimePlanIdentity(
					"lima-guest:mds",
					imageURL,
					imageSHA256,
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
					imageSHA256,
				)}
			}
			return transport.Result{}
		},
	}
	runtime := hostadapter.GuestRuntime{
		Architecture: "arm64", Port: port,
		Delegate: readyComponent{
			observation: adapters.Observation{State: adapters.StateReady},
		},
		CLIRevision:     hostRuntimeCLIRevision,
		CatalogRevision: hostRuntimeCatalogRevision,
		OwnershipRoot:   ownershipRoot,
		Spec: guest.Spec{
			WSLDistribution: "Ubuntu-26.04",
			Images: map[string]guest.ImageSpec{
				"arm64": {
					URL:    imageURL,
					SHA256: imageSHA256,
				},
			},
		},
	}
	if err := runtime.Apply(context.Background(), planning.Action{
		ID: "macos-host:local/lima", ComponentID: "lima",
	}); err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	joined := recordedArgv(port.commands)
	for _, expected := range []string{
		"limactl create --name mds -",
		"limactl start mds",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("commands do not contain %q:\n%s", expected, joined)
		}
	}
	var template string
	for _, command := range port.commands {
		if command.Executable == "limactl" &&
			len(command.Arguments) > 0 &&
			command.Arguments[0] == "create" {
			template = string(command.Stdin)
		}
	}
	if !strings.Contains(template, "location: "+imageURL) ||
		!strings.Contains(template, "digest: sha256:"+imageSHA256) ||
		!strings.Contains(template, "mode: system") ||
		!strings.Contains(template, "schema=mds.guest-image/v3") ||
		!strings.Contains(template, "image_revision=sha256:"+imageSHA256) ||
		!strings.Contains(template, "image_provenance="+imageURL) ||
		!strings.Contains(template, "creation_nonce_commitment=sha256:") ||
		strings.Count(template, "- location:") != 1 {
		t.Fatalf("Lima create template is not the reviewed one-image template:\n%s", template)
	}
}

func TestWSLRuntimeInstallsCanonicalGuestWithoutAuth(t *testing.T) {
	image := []byte("reviewed Ubuntu 26.04 WSL image")
	imageSum := sha256.Sum256(image)
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		_, _ = writer.Write(image)
	}))
	defer server.Close()
	ownershipRoot := t.TempDir()
	port := &recordingPort{
		result: func(command transport.Command) transport.Result {
			if command.Executable == "wsl.exe" &&
				len(command.Arguments) >= 2 &&
				command.Arguments[0] == "--list" {
				return transport.Result{Stdout: ""}
			}
			if isHostRuntimeMDSCommand(command) {
				return transport.Result{Stdout: hostRuntimePlanIdentity(
					"wsl-guest:Ubuntu-26.04",
					server.URL+"/ubuntu-26.04.wsl",
					hex.EncodeToString(imageSum[:]),
					ownershipNonce(
						t,
						ownershipRoot,
						"wsl",
						"Ubuntu-26.04",
					),
				)}
			}
			if isGuestImageIdentityReadCommand(command) {
				return transport.Result{Stdout: guestImageIdentityMarkerFromOwnership(
					t,
					ownershipRoot,
					"wsl",
					"Ubuntu-26.04",
					server.URL+"/ubuntu-26.04.wsl",
					hex.EncodeToString(imageSum[:]),
				)}
			}
			return transport.Result{}
		},
	}
	runtime := hostadapter.GuestRuntime{
		Architecture: "amd64", Port: port,
		Delegate: readyComponent{
			observation: adapters.Observation{State: adapters.StateAbsent},
		},
		Spec: guest.Spec{
			WSLDistribution: "Ubuntu-26.04",
			WSLImages: map[string]guest.ImageSpec{
				"amd64": {
					URL:    server.URL + "/ubuntu-26.04.wsl",
					SHA256: hex.EncodeToString(imageSum[:]),
				},
			},
		},
		Client:          server.Client(),
		CLIRevision:     hostRuntimeCLIRevision,
		CatalogRevision: hostRuntimeCatalogRevision,
		OwnershipRoot:   ownershipRoot,
	}
	if err := runtime.Apply(context.Background(), planning.Action{
		ID: "windows-host:local/wsl", ComponentID: "wsl",
	}); err != nil {
		t.Fatalf("Apply(): %v", err)
	}
	joined := recordedArgv(port.commands)
	for _, expected := range []string{
		"wsl.exe --install --no-distribution",
		"wsl.exe --install --from-file",
		"--name Ubuntu-26.04 --no-launch",
		"wsl.exe --distribution Ubuntu-26.04 --user root --exec /bin/sh",
		"/etc/mds/image-identity-v1",
		"sha256:" + hex.EncodeToString(imageSum[:]),
		server.URL + "/ubuntu-26.04.wsl",
		"wsl.exe --distribution Ubuntu-26.04 --exec /bin/sh -eu -c",
		"/usr/bin/id -u",
		"/usr/bin/cut -d: -f6",
		`[ "$uid" -ne 0 ]`,
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("commands do not contain %q:\n%s", expected, joined)
		}
	}
	if strings.Contains(joined, "--install --distribution") {
		t.Fatalf("WSL lifecycle used moving distribution install:\n%s", joined)
	}
	record, exists, err := guest.LoadOwnership(
		ownershipRoot,
		"wsl",
		"Ubuntu-26.04",
	)
	if err != nil || !exists {
		t.Fatalf("LoadOwnership() record=%+v exists=%t error=%v", record, exists, err)
	}
	if strings.Contains(joined, record.CreationNonce) {
		t.Fatal("WSL lifecycle exposed the raw guest nonce in process arguments")
	}
	commitment := hostNonceCommitment(record.CreationNonce)
	var commitmentStdinCommands int
	for _, command := range port.commands {
		if string(command.Stdin) == record.CreationNonce+"\n" {
			t.Fatal("WSL lifecycle exposed the raw guest nonce on subprocess stdin")
		}
		if string(command.Stdin) != commitment+"\n" {
			continue
		}
		commitmentStdinCommands++
	}
	if commitmentStdinCommands != 3 {
		t.Fatalf(
			"commitment stdin command count = %d, want marker write and two independent reads",
			commitmentStdinCommands,
		)
	}
	for _, forbidden := range []string{" auth ", " login ", "token"} {
		if strings.Contains(strings.ToLower(joined), forbidden) {
			t.Fatalf("WSL lifecycle contains forbidden auth surface %q:\n%s", forbidden, joined)
		}
	}
}

func TestWSLRuntimeRejectsPinnedImageChecksumMismatchBeforeInstall(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		_, _ = writer.Write([]byte("tampered WSL image"))
	}))
	defer server.Close()
	ownershipRoot := t.TempDir()
	port := &recordingPort{
		result: func(command transport.Command) transport.Result {
			if command.Executable == "wsl.exe" &&
				len(command.Arguments) >= 2 &&
				command.Arguments[0] == "--list" {
				return transport.Result{Stdout: ""}
			}
			return transport.Result{}
		},
	}
	runtime := hostadapter.GuestRuntime{
		Architecture: "amd64",
		Port:         port,
		Delegate: readyComponent{
			observation: adapters.Observation{State: adapters.StateReady},
		},
		Spec: guest.Spec{
			WSLDistribution: "Ubuntu-26.04",
			WSLImages: map[string]guest.ImageSpec{
				"amd64": {
					URL:    server.URL + "/ubuntu-26.04.wsl",
					SHA256: strings.Repeat("0", 64),
				},
			},
		},
		Client:          server.Client(),
		CLIRevision:     hostRuntimeCLIRevision,
		CatalogRevision: hostRuntimeCatalogRevision,
		OwnershipRoot:   ownershipRoot,
	}
	err := runtime.Apply(context.Background(), planning.Action{
		ID: "windows-host:local/wsl", ComponentID: "wsl",
	})
	var actionRequired *adapters.ActionRequiredError
	if err == nil || errors.As(err, &actionRequired) ||
		!strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Apply() error = %v, want hard checksum failure", err)
	}
	if strings.Contains(recordedArgv(port.commands), "--from-file") {
		t.Fatalf("WSL install executed after checksum mismatch: %+v", port.commands)
	}
}

func TestProductionHostAdapterRequiresMatchingGuestRuntimeRevision(t *testing.T) {
	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		t.Fatalf("LoadFS(): %v", err)
	}
	catalogRevision, err := catalog.Revision(environment)
	if err != nil {
		t.Fatalf("Revision(): %v", err)
	}
	image := environment.Targets["ubuntu-26.04"].Images["arm64"]
	home := t.TempDir()
	ownershipRoot := filepath.Join(
		home,
		".local",
		"state",
		"my-desk-setup",
		"guest-ownership",
	)
	if err := guest.PublishOwnership(
		ownershipRoot,
		guest.Ownership{
			Provider: "lima", Name: "mds",
			ImageURL: image.URL, ImageSHA256: image.SHA256,
		},
	); err != nil {
		t.Fatalf("PublishOwnership(): %v", err)
	}
	port := &recordingPort{
		result: func(command transport.Command) transport.Result {
			switch {
			case command.Executable == "limactl" &&
				len(command.Arguments) > 0 &&
				command.Arguments[0] == "list":
				return transport.Result{
					Stdout: `{"name":"mds","status":"Running","arch":"aarch64","limaVersion":"2.1.1"}`,
				}
			case isHostRuntimeMDSCommand(command):
				return transport.Result{Stdout: hostRuntimePlanIdentityWithRevisions(
					"lima-guest:mds",
					version.String(),
					catalogRevision,
					image.URL,
					image.SHA256,
					ownershipNonce(
						t,
						ownershipRoot,
						"lima",
						"mds",
					),
				)}
			case isGuestImageIdentityReadCommand(command):
				return transport.Result{Stdout: guestImageIdentityMarkerFromOwnership(
					t,
					ownershipRoot,
					"lima",
					"mds",
					image.URL,
					image.SHA256,
				)}
			default:
				return transport.Result{Stdout: "limactl version 2.1.1\n"}
			}
		},
	}
	component, err := hostadapter.New(
		environment,
		port,
		home,
		"darwin",
		"arm64",
		false,
	)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	observation, err := component.Observe(context.Background(), planning.Action{
		ID:          "macos-host:local/lima",
		ComponentID: "lima",
		Installer:   "brew",
		Version:     "manager-owned",
		Verification: [][]string{
			{"limactl", "--version"},
		},
	})
	if err != nil {
		t.Fatalf("Observe(): %v", err)
	}
	if observation.State != adapters.StateReady {
		t.Fatalf("observation = %+v, want matching production handoff ready", observation)
	}
	identityReads := 0
	for _, command := range port.commands {
		if isGuestImageIdentityReadCommand(command) {
			identityReads++
		}
	}
	if identityReads != 1 {
		t.Fatalf(
			"guest image identity marker reads = %d, want exactly one: %+v",
			identityReads,
			port.commands,
		)
	}
	if !isHostRuntimeMDSCommand(port.commands[len(port.commands)-1]) {
		t.Fatalf("production adapter did not execute guest-local mds: %+v", port.commands)
	}
}

func isHostRuntimeMDSCommand(command transport.Command) bool {
	for index := 0; index+1 < len(command.Arguments); index++ {
		if command.Arguments[index] == "mds" &&
			command.Arguments[index+1] == "plan" {
			return true
		}
	}
	return false
}

func hostRuntimePlanIdentity(
	targetID,
	imageURL,
	imageSHA256,
	imageCreationNonce string,
) string {
	return hostRuntimePlanIdentityWithRevisions(
		targetID,
		hostRuntimeCLIRevision,
		hostRuntimeCatalogRevision,
		imageURL,
		imageSHA256,
		imageCreationNonce,
	)
}

func hostRuntimePlanIdentityWithRevisions(
	targetID,
	cliRevision,
	catalogRevision,
	imageURL,
	imageSHA256,
	imageCreationNonce string,
) string {
	id, _ := target.ParseID(targetID)
	encoded, _ := json.Marshal(struct {
		CatalogRevision string       `json:"catalog_revision"`
		Target          target.Facts `json:"target"`
	}{
		CatalogRevision: catalogRevision,
		Target: target.Facts{
			ID:              id,
			CLIRevision:     cliRevision,
			CatalogRevision: catalogRevision,
			ImageRevision:   "sha256:" + imageSHA256,
			ImageProvenance: imageURL,
			ImageCreationNonceCommitment: hostNonceCommitment(
				imageCreationNonce,
			),
		},
	})
	return string(encoded)
}

func hostNonceCommitment(nonce string) string {
	commitment, err := target.GuestCreationNonceCommitment(nonce)
	if err != nil {
		panic(err)
	}
	return commitment
}

func ownershipNonce(
	t *testing.T,
	root,
	provider,
	name string,
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
	return record.CreationNonce
}

func TestWindowsDesktopUsesWinGetInventoryWithoutLaunchingApp(t *testing.T) {
	port := &recordingPort{
		result: func(command transport.Command) transport.Result {
			return transport.Result{
				Stdout: "Name  Id  Version\nNotion  Notion.Notion  4.0.0\n",
			}
		},
	}
	desktop := hostadapter.Desktop{
		Platform: "windows", Port: port, Delegate: readyComponent{},
	}
	observation, err := desktop.Observe(context.Background(), planning.Action{
		ComponentID: "notion-desktop",
		Package:     "Notion.Notion",
		Version:     "manager-owned",
	})
	if err != nil {
		t.Fatalf("Observe(): %v", err)
	}
	if observation.State != adapters.StateReady {
		t.Fatalf("observation = %+v, want ready", observation)
	}
	joined := recordedArgv(port.commands)
	if !strings.Contains(joined, "winget list --id Notion.Notion --exact") {
		t.Fatalf("desktop probe = %s", joined)
	}
	for _, forbidden := range []string{" open ", " start ", " login ", " auth "} {
		if strings.Contains(" "+strings.ToLower(joined)+" ", forbidden) {
			t.Fatalf("desktop probe contains forbidden operation %q: %s", forbidden, joined)
		}
	}
}
