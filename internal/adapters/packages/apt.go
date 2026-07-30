package packages

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

const (
	sudoExecutable      = "/usr/bin/sudo"
	privilegedTrue      = "/usr/bin/true"
	privilegedEnv       = "/usr/bin/env"
	privilegedAPTGet    = "/usr/bin/apt-get"
	privilegedInstall   = "/usr/bin/install"
	privilegedSystemctl = "/usr/bin/systemctl"
)

func APTInstall(action planning.Action) ([]transport.Command, error) {
	packages := strings.Fields(action.Package)
	if len(packages) == 0 {
		return nil, fmt.Errorf("APT package is required for %s", action.ID)
	}
	arguments := []string{
		"-n",
		privilegedEnv,
		"DEBIAN_FRONTEND=noninteractive",
		"APT_LISTCHANGES_FRONTEND=none",
		privilegedAPTGet,
		"install",
		"-y",
		"--no-install-recommends",
	}
	arguments = append(arguments, packages...)
	return []transport.Command{
		sudoPreflightCommand(),
		{
			Executable: sudoExecutable,
			Arguments:  arguments,
			Timeout:    45 * time.Minute,
		},
	}, nil
}

// RequireSudo verifies that the user has explicitly refreshed sudo credentials.
// mds never prompts for a password or attempts to acquire credentials itself.
func RequireSudo(ctx context.Context, port transport.Port) error {
	if port == nil {
		return fmt.Errorf("sudo preflight requires a transport port")
	}
	preflight := sudoPreflightCommand()
	if err := ValidatePrivilegedCommand(preflight); err != nil {
		return err
	}
	if _, err := port.Run(ctx, preflight); err != nil {
		return &adapters.ActionRequiredError{
			Reason: "run `sudo -v` inside the guest, then rerun the same mds apply; mds does not collect sudo credentials",
		}
	}
	return nil
}

func sudoPreflightCommand() transport.Command {
	return transport.Command{
		Executable: sudoExecutable,
		Arguments:  []string{"-n", privilegedTrue},
		Timeout:    30 * time.Second,
	}
}

func isSudoPreflight(command transport.Command) bool {
	return command.Executable == sudoExecutable &&
		len(command.Arguments) == 2 &&
		command.Arguments[0] == "-n" &&
		command.Arguments[1] == privilegedTrue
}
