package guest

import (
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func TestUnexpectedCheckoutStatusAllowsOnlyGeneratedHelpTags(t *testing.T) {
	if got := unexpectedCheckoutStatus("?? doc/tags\n"); got != "" {
		t.Fatalf("generated help tags status = %q, want allowed", got)
	}
	for _, status := range []string{
		" M lua/plugin.lua\n",
		"?? lua/injected.lua\n",
		" M doc/tags\n",
	} {
		if got := unexpectedCheckoutStatus(status); got == "" {
			t.Fatalf("checkout status %q was incorrectly allowed", status)
		}
	}
}

func TestNeovimFailureDiagnosticRejectsZeroExitStartupErrors(t *testing.T) {
	if got := neovimFailureDiagnostic(transport.Result{
		Stderr: "Error detected while processing init.lua:\nE5113: Lua chunk failed\n",
	}); got == "" {
		t.Fatal("Neovim startup error was not detected")
	}
	if got := neovimFailureDiagnostic(transport.Result{
		Stdout: "plugin restore completed\n",
	}); got != "" {
		t.Fatalf("successful Neovim output was rejected: %q", got)
	}
}
