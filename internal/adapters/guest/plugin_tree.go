package guest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/zzanghyunmoo/my-desk-setup/internal/durable"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

const pluginRuntimeSchema = "mds.nvim-runtime/v1"

var neovimFailurePattern = regexp.MustCompile(
	`(?i)(error detected while processing|E[0-9]{3,4}:)`,
)

type pluginRuntimeMarker struct {
	SchemaVersion string `json:"schema_version"`
}

func inspectPluginRuntime(
	ctx context.Context,
	home string,
	port transport.Port,
	set pluginSet,
) (bool, string, error) {
	if port == nil {
		return false, "", errors.New("plugin runtime transport is required")
	}
	root, exists, err := inspectPluginRuntimeRoot(home)
	if err != nil || !exists {
		return false, "managed plugin runtime is missing", err
	}
	lazyPath := managedLazyPath(root)
	ready, detail, err := inspectCheckout(ctx, port, lazyPath, lazyPluginCommit)
	if err != nil || !ready {
		return false, "lazy.nvim " + detail, err
	}
	pluginRoot := managedPluginRoot(root)
	for _, pin := range expectedPluginPins(set) {
		ready, detail, err := inspectCheckout(
			ctx,
			port,
			filepath.Join(pluginRoot, pin.Name),
			pin.Commit,
		)
		if err != nil || !ready {
			return false, pin.Name + " " + detail, err
		}
	}
	return true, "", nil
}

func preparePluginRuntime(
	ctx context.Context,
	home string,
	port transport.Port,
	set pluginSet,
) error {
	if port == nil {
		return errors.New("plugin runtime transport is required")
	}
	root, err := ensurePluginRuntimeRoot(home)
	if err != nil {
		return err
	}
	if err := ensureLazyCheckout(ctx, port, root); err != nil {
		return err
	}
	pluginRoot := managedPluginRoot(root)
	if err := ensureDirectory(pluginRoot); err != nil {
		return err
	}
	for _, pin := range expectedPluginPins(set) {
		path := filepath.Join(pluginRoot, pin.Name)
		ready, _, inspectErr := inspectCheckout(ctx, port, path, pin.Commit)
		if inspectErr != nil {
			return inspectErr
		}
		if ready {
			continue
		}
		if err := removeManagedCheckout(pluginRoot, path); err != nil {
			return fmt.Errorf("remove drifted plugin %s: %w", pin.Name, err)
		}
	}
	return nil
}

func verifyManagedNeovim(
	ctx context.Context,
	home string,
	port transport.Port,
	set pluginSet,
) error {
	if port == nil {
		return errors.New("Neovim verification transport is required")
	}
	root, exists, err := inspectPluginRuntimeRoot(home)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("managed plugin runtime is missing")
	}
	ready, detail, err := inspectCheckout(
		ctx,
		port,
		managedLazyPath(root),
		lazyPluginCommit,
	)
	if err != nil {
		return err
	}
	if !ready {
		return fmt.Errorf("managed lazy.nvim is not ready: %s", detail)
	}
	pluginRoot := managedPluginRoot(root)
	for _, pin := range expectedPluginPins(set) {
		path := filepath.Join(pluginRoot, pin.Name)
		info, statErr := os.Lstat(path)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return fmt.Errorf("inspect plugin %s: %w", pin.Name, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("plugin %s is not a regular directory", pin.Name)
		}
		ready, detail, err := inspectCheckout(ctx, port, path, pin.Commit)
		if err != nil {
			return err
		}
		if !ready {
			return fmt.Errorf("plugin %s is not safe to execute: %s", pin.Name, detail)
		}
	}

	var verificationOutput strings.Builder
	for _, arguments := range [][]string{
		{"--headless", "+Lazy! restore", "+qa"},
		{"--headless", "+checkhealth", "+qa"},
	} {
		result, err := port.Run(ctx, transport.Command{
			Executable:  "nvim",
			Arguments:   arguments,
			Environment: managedEditorEnvironment(home),
			Timeout:     5 * time.Minute,
			OutputLimit: transport.DefaultOutputLimit,
		})
		verificationOutput.WriteString(result.Stderr)
		verificationOutput.WriteString("\n")
		verificationOutput.WriteString(result.Stdout)
		verificationOutput.WriteString("\n")
		if err != nil {
			return fmt.Errorf("verify managed Neovim with %s: %w", arguments[1], err)
		}
		if diagnostic := neovimFailureDiagnostic(result); diagnostic != "" {
			return fmt.Errorf(
				"verify managed Neovim with %s: %s",
				arguments[1],
				diagnostic,
			)
		}
	}
	editorRoot, err := managedEditorRoot(home)
	if err != nil {
		return err
	}
	if err := writeConfigurationFiles(editorRoot, map[string]string{
		"lazy-lock.json": managedLazyLock,
	}); err != nil {
		return fmt.Errorf("restore reviewed Neovim lockfile bytes: %w", err)
	}

	ready, detail, err = inspectPluginRuntime(ctx, home, port, set)
	if err != nil {
		return err
	}
	if !ready {
		diagnostic := transport.SanitizeDiagnostic(verificationOutput.String())
		if diagnostic == "" {
			return fmt.Errorf("managed plugin graph is not ready after restore: %s", detail)
		}
		return fmt.Errorf(
			"managed plugin graph is not ready after restore: %s; Neovim output: %s",
			detail,
			diagnostic,
		)
	}
	return nil
}

func neovimFailureDiagnostic(result transport.Result) string {
	diagnostic := transport.SanitizeDiagnostic(result.Stderr + "\n" + result.Stdout)
	if neovimFailurePattern.MatchString(diagnostic) {
		return diagnostic
	}
	return ""
}

func inspectCheckout(
	ctx context.Context,
	port transport.Port,
	path,
	expectedCommit string,
) (bool, string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, "checkout is missing", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("inspect checkout %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, "", fmt.Errorf("checkout %s is not a regular directory", path)
	}
	result, err := port.Run(ctx, transport.Command{
		Executable:  "git",
		Arguments:   []string{"-C", path, "rev-parse", "HEAD"},
		Timeout:     time.Minute,
		OutputLimit: transport.DefaultOutputLimit,
	})
	if err != nil {
		return false, "checkout revision cannot be read", nil
	}
	if strings.TrimSpace(result.Stdout) != expectedCommit {
		return false, "checkout revision differs", nil
	}
	result, err = port.Run(ctx, transport.Command{
		Executable: "git",
		Arguments: []string{
			"-C", path, "status", "--porcelain", "--untracked-files=all",
		},
		Timeout:     time.Minute,
		OutputLimit: transport.DefaultOutputLimit,
	})
	if err != nil {
		return false, "checkout cleanliness cannot be read", nil
	}
	status := unexpectedCheckoutStatus(result.Stdout)
	if status != "" {
		return false, "checkout has modified or untracked files: " +
			transport.SanitizeDiagnostic(status), nil
	}
	return true, "", nil
}

func unexpectedCheckoutStatus(output string) string {
	var unexpected []string
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.TrimSpace(strings.TrimPrefix(line, "??")) == "doc/tags" {
			continue
		}
		unexpected = append(unexpected, line)
	}
	return strings.Join(unexpected, "\n")
}

func ensureLazyCheckout(
	ctx context.Context,
	port transport.Port,
	runtimeRoot string,
) error {
	lazyParent := filepath.Dir(managedLazyPath(runtimeRoot))
	if err := ensureDirectory(lazyParent); err != nil {
		return err
	}
	target := managedLazyPath(runtimeRoot)
	ready, _, err := inspectCheckout(ctx, port, target, lazyPluginCommit)
	if err != nil {
		return err
	}
	if ready {
		return nil
	}
	staging, err := os.MkdirTemp(lazyParent, ".lazy-checkout-*")
	if err != nil {
		return fmt.Errorf("create lazy.nvim staging directory: %w", err)
	}
	defer os.RemoveAll(staging)
	commands := []transport.Command{
		{Executable: "git", Arguments: []string{"init", staging}},
		{Executable: "git", Arguments: []string{
			"-C", staging, "remote", "add", "origin", "https://github.com/folke/lazy.nvim.git",
		}},
		{Executable: "git", Arguments: []string{
			"-C", staging, "fetch", "--depth", "1", "origin", lazyPluginCommit,
		}},
		{Executable: "git", Arguments: []string{
			"-C", staging, "checkout", "--detach", "FETCH_HEAD",
		}},
	}
	for _, command := range commands {
		command.Timeout = 5 * time.Minute
		command.OutputLimit = transport.DefaultOutputLimit
		if _, err := port.Run(ctx, command); err != nil {
			return fmt.Errorf("prepare exact lazy.nvim checkout: %w", err)
		}
	}
	if err := replaceManagedCheckout(lazyParent, staging, target); err != nil {
		return fmt.Errorf("publish exact lazy.nvim checkout: %w", err)
	}
	return nil
}

func ensurePluginRuntimeRoot(home string) (string, error) {
	parent := filepath.Join(home, ".local", "share", "mds")
	if err := ensureDirectory(parent); err != nil {
		return "", err
	}
	root := filepath.Join(parent, "nvim")
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(root, 0o700); err != nil {
			return "", fmt.Errorf("create managed plugin runtime: %w", err)
		}
		content, encodeErr := pluginRuntimeMarkerBytes()
		if encodeErr != nil {
			return "", encodeErr
		}
		if err := durable.WriteFileNoReplace(
			filepath.Join(root, ".mds-managed.json"),
			content,
			0o600,
		); err != nil {
			return "", fmt.Errorf("write plugin runtime marker: %w", err)
		}
		return root, nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect managed plugin runtime: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("managed plugin runtime is not a regular directory")
	}
	_, exists, err := inspectPluginRuntimeRoot(home)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", errors.New("existing plugin runtime is not owned by mds")
	}
	return root, nil
}

func inspectPluginRuntimeRoot(home string) (string, bool, error) {
	root := filepath.Join(home, ".local", "share", "mds", "nvim")
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return root, false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("inspect plugin runtime: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", false, errors.New("plugin runtime is not a regular directory")
	}
	content, err := os.ReadFile(filepath.Join(root, ".mds-managed.json"))
	if errors.Is(err, os.ErrNotExist) {
		return root, false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read plugin runtime marker: %w", err)
	}
	expected, err := pluginRuntimeMarkerBytes()
	if err != nil {
		return "", false, err
	}
	if !bytes.Equal(content, expected) {
		return "", false, errors.New("plugin runtime ownership marker differs")
	}
	return root, true, nil
}

func pluginRuntimeMarkerBytes() ([]byte, error) {
	content, err := json.MarshalIndent(pluginRuntimeMarker{
		SchemaVersion: pluginRuntimeSchema,
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode plugin runtime marker: %w", err)
	}
	return append(content, '\n'), nil
}

func managedLazyPath(runtimeRoot string) string {
	return filepath.Join(runtimeRoot, "l", lazyPluginCommit[:12])
}

func managedPluginRoot(runtimeRoot string) string {
	return filepath.Join(runtimeRoot, "p", managedPluginGraphID)
}

func replaceManagedCheckout(parent, staging, target string) error {
	var previous string
	if _, err := os.Lstat(target); err == nil {
		reserved, reserveErr := os.MkdirTemp(parent, ".replaced-checkout-*")
		if reserveErr != nil {
			return reserveErr
		}
		if err := os.Remove(reserved); err != nil {
			return err
		}
		if err := os.Rename(target, reserved); err != nil {
			return err
		}
		previous = reserved
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(staging, target); err != nil {
		if previous != "" {
			_ = os.Rename(previous, target)
		}
		return err
	}
	if previous != "" {
		return os.RemoveAll(previous)
	}
	return nil
}

func removeManagedCheckout(parent, target string) error {
	tombstone, err := os.MkdirTemp(parent, ".removed-checkout-*")
	if err != nil {
		return err
	}
	if err := os.Remove(tombstone); err != nil {
		return err
	}
	if err := os.Rename(target, tombstone); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return os.RemoveAll(tombstone)
}

func managedEditorEnvironment(home string) map[string]string {
	bunHome := filepath.Join(home, ".local", "share", "bun")
	miseHome := filepath.Join(home, ".local", "share", "mise")
	return map[string]string{
		"HOME": home,
		"PATH": strings.Join([]string{
			filepath.Join(home, ".local", "bin"),
			filepath.Join(bunHome, "bin"),
			filepath.Join(miseHome, "shims"),
			os.Getenv("PATH"),
		}, string(os.PathListSeparator)),
		"BUN_INSTALL":     bunHome,
		"MISE_DATA_DIR":   miseHome,
		"MISE_CONFIG_DIR": filepath.Join(home, ".config", "mise"),
	}
}
