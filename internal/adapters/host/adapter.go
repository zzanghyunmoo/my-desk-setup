package host

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	guestadapter "github.com/zzanghyunmoo/my-desk-setup/internal/adapters/guest"
	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/packages"
	"github.com/zzanghyunmoo/my-desk-setup/internal/artifact"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	harnessruntime "github.com/zzanghyunmoo/my-desk-setup/internal/harness"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
	"github.com/zzanghyunmoo/my-desk-setup/internal/version"
)

type Options struct {
	AllowReplace          bool
	GuestBootstrapArchive string
	HarnessAcquirer       planning.SnapshotAcquirer
	HarnessRuntime        HarnessRuntime
	HarnessConfigRoot     string
	HarnessTempRoot       string
	HarnessStateRoot      string
	HarnessLocale         string
	SystemRoot            string
	ComSpec               string
	AppData               string
	LocalAppData          string
	PathExt               string
}

type GuestBootstrapArchiveError struct {
	err error
}

func (err *GuestBootstrapArchiveError) Error() string {
	return err.err.Error()
}

func (err *GuestBootstrapArchiveError) Unwrap() error {
	return err.err
}

func guestBootstrapArchiveError(err error) error {
	return &GuestBootstrapArchiveError{err: err}
}

func New(
	environment catalog.Environment,
	port transport.Port,
	home,
	platform,
	architecture string,
	allowReplace bool,
) (adapters.Component, error) {
	return NewWithOptions(
		environment,
		port,
		home,
		platform,
		architecture,
		Options{AllowReplace: allowReplace},
	)
}

func NewWithOptions(
	environment catalog.Environment,
	port transport.Port,
	home,
	platform,
	architecture string,
	options Options,
) (adapters.Component, error) {
	packagesAdapter := packages.Adapter{
		Environment:  environment,
		Port:         port,
		Home:         home,
		AllowReplace: options.AllowReplace,
		Vendor: packages.Vendor{
			Home: home, Platform: platform, Arch: architecture,
		},
	}
	packageComponent := adapters.Component(packagesAdapter)
	if platform == "darwin" {
		packageComponent = packages.HomebrewPrerequisite{
			Port: port, Delegate: packagesAdapter,
		}
	}
	spec, exists := environment.Targets["ubuntu-26.04"]
	if !exists {
		return nil, fmt.Errorf("catalog target %q is required", "ubuntu-26.04")
	}
	catalogRevision, err := catalog.Revision(environment)
	if err != nil {
		return nil, err
	}
	runtime := GuestRuntime{
		Architecture: architecture, Port: port,
		Delegate: packageComponent, Spec: spec,
		CLIRevision: version.String(), CatalogRevision: catalogRevision,
		OwnershipRoot: filepath.Join(
			home, ".local", "state", "my-desk-setup", "guest-ownership",
		),
	}
	for _, guestArchitecture := range []string{"amd64", "arm64"} {
		artifact, exists := version.GuestLinuxArtifact(guestArchitecture)
		if !exists {
			continue
		}
		if runtime.BootstrapArtifacts == nil {
			runtime.BootstrapArtifacts = make(map[string]GuestBootstrapArtifact)
		}
		runtime.BootstrapArtifacts[guestArchitecture] = GuestBootstrapArtifact{
			URL: artifact.URL, SHA256: artifact.SHA256,
		}
	}
	if options.GuestBootstrapArchive != "" {
		guestArchitecture := normalizeCatalogArchitecture(architecture)
		artifact, exists := runtime.BootstrapArtifacts[guestArchitecture]
		if !exists {
			return nil, guestBootstrapArchiveError(fmt.Errorf(
				"guest bootstrap metadata is unavailable for host architecture %q",
				guestArchitecture,
			))
		}
		snapshot, err := loadGuestBootstrapArchive(
			options.GuestBootstrapArchive,
			artifact.SHA256,
		)
		if err != nil {
			return nil, guestBootstrapArchiveError(err)
		}
		artifact.Archive = snapshot
		runtime.BootstrapArtifacts[guestArchitecture] = artifact
	}
	byID := map[string]adapters.Component{
		"lima": runtime,
		"wsl":  runtime,
	}
	desktop := Desktop{
		Platform: platform, Port: port, Delegate: packageComponent,
	}
	for _, componentID := range []string{
		"notion-desktop", "linear-desktop", "slack", "kakaotalk", "chrome",
	} {
		byID[componentID] = desktop
	}
	if platform == "darwin" {
		for _, componentID := range []string{"claude-code", "opencode", "codex"} {
			byID[componentID] = guestadapter.Agent{
				Home: home, Delegate: packageComponent,
			}
		}
	}
	tempRoot := options.HarnessTempRoot
	if tempRoot == "" {
		tempRoot = os.TempDir()
	}
	configRoot := options.HarnessConfigRoot
	if configRoot == "" {
		configRoot = filepath.Join(home, ".config")
	}
	stateRoot := options.HarnessStateRoot
	if stateRoot == "" {
		stateRoot = filepath.Join(
			home, ".local", "state", "my-desk-setup", "oh-my-harness",
		)
	}
	acquirer := options.HarnessAcquirer
	if acquirer == nil {
		acquirer = planning.ArtifactSnapshotAcquirer{
			Snapshotter: artifact.Snapshotter{TempRoot: tempRoot},
		}
	}
	childRuntime := options.HarnessRuntime
	if childRuntime == nil {
		childRuntime = harnessruntime.Runner{Port: port}
	}
	byID["oh-my-harness"] = &Harness{
		Environment: environment,
		Composer: planning.Composer{
			Acquirer: acquirer, Previewer: childRuntime,
			Home: home, ConfigRoot: configRoot, TempRoot: tempRoot,
			StateRoot: stateRoot, Locale: options.HarnessLocale,
			SystemRoot: options.SystemRoot, ComSpec: options.ComSpec,
			AppData: options.AppData, LocalAppData: options.LocalAppData,
			PathExt: options.PathExt, Timeout: 45 * time.Second,
		},
		Runtime: childRuntime, Home: home, Platform: platform,
	}
	return adapters.Router{
		Default: packageComponent,
		ByID:    byID,
	}, nil
}
