package guest

import (
	"net/http"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/packages"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

// New returns the production component router for one Linux guest. All
// commands are executed through the supplied guest-local transport port.
func New(
	environment catalog.Environment,
	facts target.Facts,
	port transport.Port,
	home,
	platform,
	architecture string,
	client *http.Client,
	now func() time.Time,
	allowReplace bool,
	allowAdopt bool,
) adapters.Component {
	packageAdapter := packages.Adapter{
		Environment:  environment,
		Port:         port,
		Home:         home,
		AllowReplace: allowReplace,
		Vendor: packages.Vendor{
			Client: client, Home: home, Platform: platform, Arch: architecture,
		},
	}
	return adapters.Router{
		Default: packageAdapter,
		ByID: map[string]adapters.Component{
			"claude-code": Agent{Home: home, Delegate: packageAdapter},
			"opencode":    Agent{Home: home, Delegate: packageAdapter},
			"codex":       Agent{Home: home, Delegate: packageAdapter},
			"nvchad": Editor{
				Home: home, Port: port, Delegate: packageAdapter, Now: now,
				AllowReplace: allowReplace, AllowAdopt: allowAdopt,
			},
			"docker-engine": Docker{
				Facts: facts, Port: port, Delegate: packageAdapter, Client: client,
			},
		},
	}
}
