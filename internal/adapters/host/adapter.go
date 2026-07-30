package host

import (
	catalogdata "github.com/zzanghyunmoo/my-desk-setup/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	guestadapter "github.com/zzanghyunmoo/my-desk-setup/internal/adapters/guest"
	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/packages"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/guest"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
	"github.com/zzanghyunmoo/my-desk-setup/internal/version"
)

func New(
	environment catalog.Environment,
	port transport.Port,
	home,
	platform,
	architecture string,
	allowReplace bool,
) (adapters.Component, error) {
	packagesAdapter := packages.Adapter{
		Environment:  environment,
		Port:         port,
		Home:         home,
		AllowReplace: allowReplace,
		Vendor: packages.Vendor{
			Home: home, Platform: platform, Arch: architecture,
		},
	}
	spec, err := guest.LoadSpecFS(catalogdata.FS, "targets/ubuntu-26.04.yaml")
	if err != nil {
		return nil, err
	}
	catalogRevision, err := catalog.Revision(environment)
	if err != nil {
		return nil, err
	}
	runtime := GuestRuntime{
		Architecture: architecture, Port: port,
		Delegate: packagesAdapter, Spec: spec,
		CLIRevision: version.String(), CatalogRevision: catalogRevision,
	}
	byID := map[string]adapters.Component{
		"lima": runtime,
		"wsl":  runtime,
	}
	desktop := Desktop{
		Platform: platform, Port: port, Delegate: packagesAdapter,
	}
	for _, componentID := range []string{
		"notion-desktop", "linear-desktop", "slack", "kakaotalk", "chrome",
	} {
		byID[componentID] = desktop
	}
	if platform == "darwin" {
		for _, componentID := range []string{"claude-code", "opencode", "codex"} {
			byID[componentID] = guestadapter.Agent{
				Home: home, Delegate: packagesAdapter,
			}
		}
	}
	return adapters.Router{
		Default: packagesAdapter,
		ByID:    byID,
	}, nil
}
