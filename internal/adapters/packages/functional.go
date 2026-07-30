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
	commands [][]string
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
	for index, argv := range scenario.commands {
		command := transport.Command{
			Executable:       argv[0],
			Arguments:        append([]string(nil), argv[1:]...),
			Environment:      adapter.environment(),
			WorkingDirectory: root,
			Timeout:          5 * time.Minute,
			OutputLimit:      transport.DefaultOutputLimit,
		}
		result, err := adapter.execute(ctx, command)
		if err != nil {
			return fmt.Errorf(
				"functional verification for %s with %s: %w",
				action.ID,
				argv[0],
				err,
			)
		}
		if index == len(scenario.commands)-1 &&
			strings.TrimSpace(result.Stdout) != "ok" {
			return fmt.Errorf(
				"functional verification for %s returned %q instead of ok",
				action.ID,
				strings.TrimSpace(result.Stdout),
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
		commands: [][]string{
			{"javac", "Main.java"},
			{"java", "-cp", ".", "Main"},
		},
	},
	"kotlin": {
		files: map[string]string{
			"Main.kt": "fun main() { println(\"ok\") }\n",
		},
		commands: [][]string{
			{"kotlinc", "Main.kt", "-include-runtime", "-d", "main.jar"},
			{"java", "-jar", "main.jar"},
		},
	},
	"go": {
		files: map[string]string{
			"main.go": "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"ok\") }\n",
		},
		commands: [][]string{{"go", "run", "main.go"}},
	},
	"python": {
		commands: [][]string{{"python", "-c", "print('ok')"}},
	},
	"typescript": {
		files: map[string]string{
			"main.ts": "console.log('ok')\n",
		},
		commands: [][]string{
			{"tsc", "--outDir", "out", "main.ts"},
			{"bun", "out/main.js"},
		},
	},
	"c-toolchain": {
		files: map[string]string{
			"main.c": "#include <stdio.h>\nint main(void) { puts(\"ok\"); return 0; }\n",
		},
		commands: [][]string{
			{"cc", "main.c", "-o", "mds-c-smoke"},
			{"./mds-c-smoke"},
		},
	},
	"flutter": {
		files: map[string]string{
			"main.dart": "void main() { print('ok'); }\n",
		},
		commands: [][]string{{"dart", "run", "main.dart"}},
	},
	"gradle": {
		files: map[string]string{
			"build.gradle.kts": `tasks.register("mdsSmoke") {
    doLast { println("ok") }
}
`,
		},
		commands: [][]string{{
			"gradle", "--offline", "--no-daemon", "-q", "mdsSmoke",
		}},
	},
}
