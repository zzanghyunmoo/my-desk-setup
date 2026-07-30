package packages

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"

	catalogdata "github.com/zzanghyunmoo/my-desk-setup/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

var aptPackagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+:~-]*$`)

const catalogDockerGuestLocalEndpoint = "unix:///var/run/docker.sock"

var (
	reviewedCatalogVerificationCommands,
	reviewedCatalogVerificationError = loadReviewedCatalogVerificationCommands()
)

// ValidatePrivilegedCommand is the fail-closed allowlist for every privileged
// command mds may execute. Password collection and privileged shell strings
// are intentionally absent from this surface.
func ValidatePrivilegedCommand(command transport.Command) error {
	executableBase := filepath.Base(command.Executable)
	if executableBase == "sudo" && command.Executable != sudoExecutable {
		return fmt.Errorf(
			"privileged executable %q must use the reviewed absolute path %q",
			command.Executable,
			sudoExecutable,
		)
	}
	switch executableBase {
	case "sh", "bash", "dash", "zsh", "fish", "env":
		return fmt.Errorf(
			"command wrapper %q is outside the reviewed verification allowlist",
			command.Executable,
		)
	}
	if command.Executable != sudoExecutable {
		return nil
	}
	if len(command.Arguments) < 2 || command.Arguments[0] != "-n" {
		return errors.New("privileged command requires noninteractive sudo -n")
	}
	arguments := command.Arguments[1:]
	switch arguments[0] {
	case privilegedTrue:
		if len(arguments) != 1 {
			return errors.New("sudo preflight does not accept extra arguments")
		}
	case privilegedEnv:
		if err := validatePrivilegedAPT(arguments[1:]); err != nil {
			return err
		}
	case privilegedInstall:
		if !allowedInstallArguments(arguments[1:]) {
			return errors.New("privileged install command is outside the reviewed filesystem allowlist")
		}
	case privilegedSystemctl:
		if !slices.Equal(arguments[1:], []string{"enable", "--now", "docker"}) {
			return errors.New("privileged systemctl command is outside the reviewed service allowlist")
		}
	default:
		return fmt.Errorf(
			"privileged executable %q is outside the reviewed allowlist",
			arguments[0],
		)
	}
	return nil
}

// ValidateCatalogVerificationCommand keeps catalog-originated observation and
// verification separate from the typed privileged installer surface. The
// catalog may invoke ordinary binaries directly, but it cannot select sudo,
// command wrappers, or arbitrary interpreter source.
func ValidateCatalogVerificationCommand(
	componentID string,
	command transport.Command,
) error {
	executableBase := filepath.Base(command.Executable)
	if command.Executable != executableBase ||
		executableBase == "" ||
		executableBase == "." {
		return fmt.Errorf(
			"component %q verification executable %q must be an unqualified reviewed command",
			componentID,
			command.Executable,
		)
	}
	if reviewedCatalogVerificationError != nil {
		return fmt.Errorf(
			"load reviewed catalog verification contracts: %w",
			reviewedCatalogVerificationError,
		)
	}
	approved, exists := reviewedCatalogVerificationCommands[componentID]
	if !exists {
		return fmt.Errorf(
			"component %q has no reviewed v1 catalog verification contract",
			componentID,
		)
	}
	signature, err := catalogVerificationSignature(command)
	if err != nil {
		return err
	}
	if !approved[signature] {
		return fmt.Errorf(
			"component %q verification argv is not an exact reviewed v1 catalog probe",
			componentID,
		)
	}
	return nil
}

func loadReviewedCatalogVerificationCommands() (
	map[string]map[string]bool,
	error,
) {
	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		return nil, err
	}
	commands := make(map[string]map[string]bool, len(environment.Catalog.Components))
	for _, component := range environment.Catalog.Components {
		approved := make(map[string]bool, 2)
		for _, argv := range [][]string{
			component.Verification.Command,
			component.Verification.Functional,
		} {
			if len(argv) == 0 {
				continue
			}
			signature, err := catalogVerificationSignature(transport.Command{
				Executable: argv[0],
				Arguments:  argv[1:],
			})
			if err != nil {
				return nil, fmt.Errorf(
					"component %q verification contract: %w",
					component.ID,
					err,
				)
			}
			approved[signature] = true
			if component.ID == "docker-engine" && argv[0] == "docker" {
				localDocker := append(
					[]string{
						"docker",
						"--host",
						catalogDockerGuestLocalEndpoint,
					},
					argv[1:]...,
				)
				signature, err = catalogVerificationSignature(transport.Command{
					Executable: localDocker[0],
					Arguments:  localDocker[1:],
				})
				if err != nil {
					return nil, fmt.Errorf(
						"component %q guest-local verification contract: %w",
						component.ID,
						err,
					)
				}
				approved[signature] = true
			}
		}
		commands[component.ID] = approved
	}
	return commands, nil
}

func catalogVerificationSignature(command transport.Command) (string, error) {
	argv := append(
		[]string{command.Executable},
		command.Arguments...,
	)
	encoded, err := json.Marshal(argv)
	if err != nil {
		return "", fmt.Errorf("encode catalog verification argv: %w", err)
	}
	return string(encoded), nil
}

func validatePrivilegedAPT(arguments []string) error {
	for len(arguments) > 0 && arguments[0] != privilegedAPTGet {
		switch arguments[0] {
		case "DEBIAN_FRONTEND=noninteractive", "APT_LISTCHANGES_FRONTEND=none":
			arguments = arguments[1:]
		default:
			return fmt.Errorf(
				"privileged environment entry %q is not allowed",
				arguments[0],
			)
		}
	}
	if len(arguments) < 2 || arguments[0] != privilegedAPTGet {
		return errors.New("privileged env may execute only apt-get")
	}
	switch arguments[1] {
	case "update":
		if len(arguments) != 2 {
			return errors.New("privileged apt-get update does not accept extra arguments")
		}
	case "install":
		if len(arguments) < 5 ||
			!slices.Equal(
				arguments[2:4],
				[]string{"-y", "--no-install-recommends"},
			) {
			return errors.New("privileged apt-get install options are not reviewed")
		}
		for _, packageName := range arguments[4:] {
			if !aptPackagePattern.MatchString(packageName) {
				return fmt.Errorf("invalid privileged apt package %q", packageName)
			}
		}
	default:
		return fmt.Errorf("privileged apt-get action %q is not allowed", arguments[1])
	}
	return nil
}

func allowedInstallArguments(arguments []string) bool {
	if slices.Equal(
		arguments,
		[]string{"-d", "-m", "0755", "/etc/apt/keyrings"},
	) {
		return true
	}
	if len(arguments) != 4 ||
		arguments[0] != "-m" ||
		arguments[1] != "0644" ||
		!filepath.IsAbs(arguments[2]) {
		return false
	}
	switch arguments[3] {
	case "/etc/apt/keyrings/docker.asc",
		"/etc/apt/sources.list.d/docker.list":
		return true
	default:
		return false
	}
}
