package packages

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

type functionalScenario struct {
	files    map[string]string
	commands []functionalCommand
}

type functionalCommand struct {
	argv           []string
	expectedStdout string
}

func functionalStep(argv ...string) functionalCommand {
	return functionalCommand{argv: argv}
}

func functionalOutput(expected string, argv ...string) functionalCommand {
	return functionalCommand{argv: argv, expectedStdout: expected}
}

func (adapter Adapter) verifyFunctionalToolchain(
	ctx context.Context,
	action planning.Action,
) (returnErr error) {
	scenario, exists := functionalScenarios[action.ComponentID]
	if !exists {
		return nil
	}
	root, err := os.MkdirTemp("", "mds-functional-"+action.ComponentID+"-*")
	if err != nil {
		return fmt.Errorf("create functional verification directory: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, os.RemoveAll(root))
	}()
	for name, content := range scenario.files {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return fmt.Errorf("write functional verification source %s: %w", name, err)
		}
	}
	for _, step := range scenario.commands {
		command := transport.Command{
			Executable:       step.argv[0],
			Arguments:        append([]string(nil), step.argv[1:]...),
			Environment:      adapter.environment(),
			WorkingDirectory: root,
			Timeout:          5 * time.Minute,
			OutputLimit:      transport.DefaultOutputLimit,
		}
		command = adapter.commandWithManagedLauncher(action, command)
		result, err := adapter.execute(ctx, command)
		if err != nil {
			return fmt.Errorf(
				"functional verification for %s with %s: %w",
				action.ID,
				step.argv[0],
				err,
			)
		}
		if step.expectedStdout != "" &&
			strings.TrimSpace(result.Stdout) != step.expectedStdout {
			return fmt.Errorf(
				"functional verification for %s returned %q instead of %q",
				action.ID,
				strings.TrimSpace(result.Stdout),
				step.expectedStdout,
			)
		}
	}
	return nil
}

var functionalScenarios = map[string]functionalScenario{
	"java": {
		files: map[string]string{
			"Main.java": `public class Main {
  public static void main(String[] args) { System.out.println("ok"); }
}
`,
		},
		commands: []functionalCommand{
			functionalStep("javac", "Main.java"),
			functionalOutput("ok", "java", "-cp", ".", "Main"),
		},
	},
	"kotlin": {
		files: map[string]string{
			"Main.kt": "fun main() { println(\"ok\") }\n",
		},
		commands: []functionalCommand{
			functionalStep("kotlinc", "Main.kt", "-include-runtime", "-d", "main.jar"),
			functionalOutput("ok", "java", "-jar", "main.jar"),
		},
	},
	"go": {
		files: map[string]string{
			"main.go": "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"ok\") }\n",
		},
		commands: []functionalCommand{functionalOutput("ok", "go", "run", "main.go")},
	},
	"python": {
		commands: []functionalCommand{functionalOutput("ok", "python", "-c", "print('ok')")},
	},
	"typescript": {
		files: map[string]string{
			"main.ts": "console.log('ok')\n",
		},
		commands: []functionalCommand{
			functionalStep("tsc", "--outDir", "out", "main.ts"),
			functionalOutput("ok", "bun", "out/main.js"),
		},
	},
	"c-toolchain": {
		files: map[string]string{
			"main.c":   "#include <stdio.h>\nint main(void) { puts(\"ok\"); return 0; }\n",
			"main.cpp": "#include <iostream>\nint main() { std::cout << \"ok\\n\"; }\n",
		},
		commands: []functionalCommand{
			functionalStep("cc", "main.c", "-o", "mds-c-smoke"),
			functionalOutput("ok", "./mds-c-smoke"),
			functionalStep("c++", "main.cpp", "-o", "mds-cxx-smoke"),
			functionalOutput("ok", "./mds-cxx-smoke"),
		},
	},
	"flutter": {
		files: map[string]string{
			"main.dart": "void main() { print('ok'); }\n",
		},
		commands: []functionalCommand{functionalOutput("ok", "dart", "run", "main.dart")},
	},
	"gradle": {
		files: map[string]string{
			"build.gradle.kts": `tasks.register("mdsSmoke") {
    doLast { println("ok") }
}
`,
		},
		commands: []functionalCommand{functionalOutput(
			"ok", "gradle", "--offline", "--no-daemon", "-q", "mdsSmoke",
		)},
	},
	"nvim-ide-tools": {
		commands: []functionalCommand{
			functionalStep("clang-format", "--version"),
			functionalStep("clang-tidy", "--version"),
			functionalStep("lldb-dap", "--version"),
			functionalStep("dlv", "version"),
			functionalStep("ruff", "--version"),
			functionalOutput("ok", "/usr/bin/python3", "-c", "import debugpy; print('ok')"),
		},
	},
}
