package packages

import (
	"fmt"

	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

var homebrewCasks = map[string]bool{
	"chrome":         true,
	"kakaotalk":      true,
	"linear-desktop": true,
	"notion-desktop": true,
	"slack":          true,
	"wezterm":        true,
}

func HomebrewInstall(action planning.Action) (transport.Command, error) {
	if action.Package == "" {
		return transport.Command{}, fmt.Errorf("Homebrew package is required for %s", action.ID)
	}
	arguments := []string{"install"}
	if homebrewCasks[action.ComponentID] {
		arguments = append(arguments, "--cask")
	}
	arguments = append(arguments, action.Package)
	return transport.Command{
		Executable: "brew",
		Arguments:  arguments,
		Environment: map[string]string{
			"HOMEBREW_NO_AUTO_UPDATE":     "1",
			"HOMEBREW_NO_INSTALL_UPGRADE": "1",
			"HOMEBREW_NO_ENV_HINTS":       "1",
			"HOMEBREW_NO_ANALYTICS":       "1",
		},
	}, nil
}
