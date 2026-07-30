package contracts_test

import (
	"strings"
	"testing"

	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
)

func TestBunManagedLockRequiresValidatedNPMArtifact(t *testing.T) {
	environment := validEnvironment()
	component := &environment.Catalog.Components[0]
	for target, support := range component.Targets {
		support.Installer = "bun"
		component.Targets[target] = support
	}

	assertValidationError(t, environment, "requires exact npm tarball")

	entry := environment.Lock.Versions["fixture"]
	entry.NPM = &catalog.NPMArtifact{
		Tarball:   "https://registry.npmjs.org/fixture/-/fixture-1.0.0.tgz",
		Integrity: "sha512-not-base64",
		SHA256:    strings.Repeat("0", 64),
	}
	environment.Lock.Versions["fixture"] = entry
	assertValidationError(t, environment, "SRI")
}

func TestNonBunLockRejectsNPMArtifact(t *testing.T) {
	environment := validEnvironment()
	entry := environment.Lock.Versions["fixture"]
	entry.NPM = &catalog.NPMArtifact{
		Tarball: "https://registry.npmjs.org/fixture/-/fixture-1.0.0.tgz",
		Integrity: "sha512-" +
			"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" +
			"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAA==",
		SHA256: strings.Repeat("0", 64),
	}
	environment.Lock.Versions["fixture"] = entry

	assertValidationError(t, environment, "cannot declare npm tarball")
}
