package packages

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	catalogdata "github.com/zzanghyunmoo/my-desk-setup/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func TestMiseInstallUsesStrictLockedPackageAndReshim(t *testing.T) {
	commands, err := MiseInstall(planning.Action{
		ID:      "lima-guest:mds/go",
		Package: "go",
		Version: "1.26.5",
		Inputs: map[string]string{
			"artifact_sha256": "reviewed",
			"artifact_url":    "https://example.com/go.tar.gz",
			"mise_ref":        "1.26.5",
		},
	}, map[string]string{"MISE_CONFIG_DIR": "/reviewed"})
	if err != nil {
		t.Fatalf("MiseInstall(): %v", err)
	}
	if len(commands) != 2 ||
		commands[0].Executable != "mise" ||
		!reflect.DeepEqual(
			commands[0].Arguments,
			[]string{"install", "--locked", "go"},
		) ||
		commands[1].Executable != "mise" ||
		!reflect.DeepEqual(commands[1].Arguments, []string{"reshim"}) {
		t.Fatalf("commands = %+v, want strict locked install and reshim", commands)
	}
}

func TestToolchainVerificationCompilesAndRunsFixedSources(t *testing.T) {
	identityCommands := map[string][]string{
		"java":           {"java", "--version"},
		"kotlin":         {"kotlinc", "-version"},
		"go":             {"go", "version"},
		"python":         {"python", "--version"},
		"typescript":     {"tsc", "--version"},
		"c-toolchain":    {"cc", "--version"},
		"flutter":        {"flutter", "--version"},
		"gradle":         {"gradle", "--version"},
		"nvim-ide-tools": {"clangd", "--version"},
	}
	for componentID, scenario := range functionalScenarios {
		componentID := componentID
		scenario := scenario
		t.Run(componentID, func(t *testing.T) {
			port := &functionalRecordingPort{}
			adapter := Adapter{Home: t.TempDir(), Port: port}
			action := planning.Action{
				ID:           "lima-guest:mds/" + componentID,
				ComponentID:  componentID,
				Verification: [][]string{identityCommands[componentID]},
			}
			if err := adapter.Verify(context.Background(), action); err != nil {
				t.Fatalf("Verify(): %v", err)
			}
			if got, want := len(port.commands), len(scenario.commands)+1; got != want {
				t.Fatalf("commands = %d, want identity plus %d functional", got, len(scenario.commands))
			}
			for _, command := range port.commands[1:] {
				if command.WorkingDirectory == "" ||
					command.Timeout <= 0 ||
					command.OutputLimit <= 0 {
					t.Fatalf("functional command is not bounded: %+v", command)
				}
			}
		})
	}
}

func TestFunctionalScenariosCoverEveryCompileRunComponent(t *testing.T) {
	for _, componentID := range []string{
		"java",
		"kotlin",
		"go",
		"python",
		"typescript",
		"c-toolchain",
		"flutter",
		"gradle",
		"nvim-ide-tools",
	} {
		if _, exists := functionalScenarios[componentID]; !exists {
			t.Errorf("functional scenario is missing for %s", componentID)
		}
	}
}

func TestIDEAndCToolchainFunctionalScenariosExerciseReviewedRuntimes(t *testing.T) {
	cCommands := functionalScenarios["c-toolchain"].commands
	if !reflect.DeepEqual(cCommands, []functionalCommand{
		functionalStep("cc", "main.c", "-o", "mds-c-smoke"),
		functionalOutput("ok", "./mds-c-smoke"),
		functionalStep("c++", "main.cpp", "-o", "mds-cxx-smoke"),
		functionalOutput("ok", "./mds-cxx-smoke"),
	}) {
		t.Fatalf("C/C++ functional commands = %#v", cCommands)
	}
	ideCommands := functionalScenarios["nvim-ide-tools"].commands
	if got := ideCommands[len(ideCommands)-1]; !reflect.DeepEqual(got, functionalOutput(
		"ok", "/usr/bin/python3", "-c", "import debugpy; print('ok')",
	)) {
		t.Fatalf("debugpy verification command = %#v, want system Python", got)
	}
}

func TestPublishMiseConfigUsesReviewedEnvironmentBytes(t *testing.T) {
	environment, err := catalog.LoadFS(catalogdata.FS)
	if err != nil {
		t.Fatalf("LoadFS(): %v", err)
	}
	home := t.TempDir()
	if err := PublishMiseConfig(home, environment.Mise); err != nil {
		t.Fatalf("PublishMiseConfig(): %v", err)
	}
	for path, want := range map[string]string{
		filepath.Join(home, ".config", "mise", "config.toml"): environment.Mise.Config,
		filepath.Join(home, ".config", "mise", "mise.lock"):   environment.Mise.Lock,
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", path, err)
		}
		if string(got) != want {
			t.Fatalf("%s differs from reviewed environment bytes", path)
		}
	}
}

func TestPublishMiseConfigPreflightsBothFilesBeforeMutation(t *testing.T) {
	t.Parallel()
	for _, conflict := range []string{"config.toml", "mise.lock"} {
		conflict := conflict
		t.Run(conflict, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			directory := filepath.Join(home, ".config", "mise")
			if err := os.MkdirAll(directory, 0o700); err != nil {
				t.Fatalf("MkdirAll(): %v", err)
			}
			conflictPath := filepath.Join(directory, conflict)
			if err := os.WriteFile(
				conflictPath,
				[]byte("user-owned\n"),
				0o600,
			); err != nil {
				t.Fatalf("WriteFile(conflict): %v", err)
			}
			err := PublishMiseConfig(home, catalog.MiseFiles{
				Config: "[tools]\ngo = \"1.26.5\"\n",
				Lock:   "[[tools.go]]\nversion = \"1.26.5\"\n",
			})
			if err == nil {
				t.Fatal("PublishMiseConfig() accepted user-owned file")
			}
			other := "mise.lock"
			if conflict == other {
				other = "config.toml"
			}
			if _, statErr := os.Lstat(
				filepath.Join(directory, other),
			); !os.IsNotExist(statErr) {
				t.Fatalf("companion file was mutated before conflict: %v", statErr)
			}
		})
	}
}

type functionalRecordingPort struct {
	commands []transport.Command
}

func (port *functionalRecordingPort) Run(
	_ context.Context,
	command transport.Command,
) (transport.Result, error) {
	port.commands = append(port.commands, command)
	return transport.Result{Stdout: "ok\n"}, nil
}
