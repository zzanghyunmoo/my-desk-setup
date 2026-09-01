package contracts_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/zzanghyunmoo/my-desk-setup/internal/adapters/packages"
	"github.com/zzanghyunmoo/my-desk-setup/internal/catalog"
	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
	"github.com/zzanghyunmoo/my-desk-setup/internal/target"
	"github.com/zzanghyunmoo/my-desk-setup/internal/transport"
)

func TestCatalogLoadsAndRevisionIsCanonical(t *testing.T) {
	environment := loadCatalog(t)
	first, err := catalog.Revision(environment)
	if err != nil {
		t.Fatalf("first revision: %v", err)
	}

	reversed := environment
	reversed.Catalog.Components = append(
		[]catalog.Component(nil),
		environment.Catalog.Components...,
	)
	for left, right := 0, len(reversed.Catalog.Components)-1; left < right; left, right = left+1, right-1 {
		reversed.Catalog.Components[left], reversed.Catalog.Components[right] =
			reversed.Catalog.Components[right], reversed.Catalog.Components[left]
	}
	owner := reversed.Profiles["owner"]
	owner.Selection = append([]string(nil), owner.Selection...)
	for left, right := 0, len(owner.Selection)-1; left < right; left, right = left+1, right-1 {
		owner.Selection[left], owner.Selection[right] = owner.Selection[right], owner.Selection[left]
	}
	reversed.Profiles = copyProfiles(reversed.Profiles)
	reversed.Profiles["owner"] = owner

	second, err := catalog.Revision(reversed)
	if err != nil {
		t.Fatalf("second revision: %v", err)
	}
	if first != second {
		t.Fatalf("revision changed after reordering: %q != %q", first, second)
	}
	if !strings.HasPrefix(first, "sha256:") {
		t.Fatalf("revision = %q, want sha256 prefix", first)
	}
}

func TestLimaIDECohortIsExactAndComplete(t *testing.T) {
	environment := loadCatalog(t)
	want := map[string]struct {
		version  string
		archive  string
		manifest string
		launcher string
		usage    string
	}{
		"jdt-language-server":          {"1.60.0+202606262232", "e94c303d8198f977930803582738771fd18c52c5492878410bf222b1aa81ef1d", "84747fbc6e7c28c1ce432d4dc618034adf0331f9b4c2f5f7800d694c6763681d", "ed0980ea2da080b79566b24b67f94f510d5001eeb401c7ed316748c0b03fbfee", "direct"},
		"kotlin-debug-adapter":         {"0.4.4", "3874cbaded0fdb8229a381167895b0a6caf88b7adffabc690fcf5a6fb65d11b6", "a0389be2cd2a45f851a20788a982966de9656025c7d676dc957220f0c560f86e", "9b059a1d98ca34cd34fa6552d3efef3748d98bf3e51ba719c4d0eb1ff061e6b9", "direct"},
		"kotlin-language-server":       {"262.9593.0", "c9c11d98194c72fd1056ca58d1434e9ce708b9f524e9c6b356ca3085c6db60fa", "5be1b270bc47a438ea197d9b16960de68b835e1297836ab306b44aac218c9b61", "a40d9d0315588cd99b0b05a86f06530e565af1195b065a04f5f3b4f56dcc1c9c", "direct"},
		"spring-tools-language-server": {"5.2.0.RELEASE", "70943c4e434d469090f8cee54dacf1de10ec1161f92685581dc2ef6164971bb3", "b8bd2f537de94a3ca188a9826c093dad6ea0e132803cbdcf2016e4a1009a2207", "ec922c593895331943ee1eccda434461da034bb87ac20f406fd7fb5e211bc8e1", "resource"},
		"roslyn-language-server":       {"5.11.0-1.26380.4", "541ab1dc23848a77053e248c58ec2398c84cabb432a01a90bfab0843f1e218a0", "2b711088879509fa450a5a3f6177cdfb30fa014f0d2338eda27c3499defcc446", "4888faba7578ae77fe60f5b6c20e175ed8864f7bf49619c763e386d96c6d7969", "direct"},
		"netcoredbg":                   {"3.2.0-1092", "065ff49badec8a695dbea2de6ab6a330c774a191e426a217ab8cc05250627ccb", "150314a56915277e9cec2683a85c1aa95b068d02d1cd560b59e46563a57939ea", "d2ea6a92951c1e7db6554568000c43017f4e5328cbb1157e92d7e9fef7ae198e", "direct"},
		"java-debug-server":            {"0.53.2", "87627e24dbb5b01137decc0265f043cb08adad22af3c195f1ba39898dafb1588", "79674f0c92dfe4693bee14db50eb28ade6ad98b35648967512713ca5c7b44cd3", "69128bebd6c46d3a9daeeb77a3bbe877423c2f66eb773fd1ae5375658ce0c9bf", "resource"},
		"java-test-server":             {"0.46.0", "56c1e14dc73a30e9574c47042106fa52893bf8325b580f47c14ace07d5eef255", "adee0a1fc322436e1f0144e3af4f00f9a19dfe5f70e030b6e6bd6d1868dcbe0d", "e01cc491d866f996499e8ee55d0c3d8ba4f6ec44053975b791e3d0d2c76044e6", "resource"},
	}
	for key, expected := range want {
		entry, exists := environment.Lock.Versions[key]
		if !exists {
			t.Fatalf("missing exact cohort lock %q", key)
		}
		artifactValue, exists := entry.Artifacts["linux-arm64"]
		if !exists || artifactValue.Tree == nil {
			t.Fatalf("lock %q has no linux-arm64 runtime tree", key)
		}
		if entry.Version != expected.version || artifactValue.SHA256 != expected.archive ||
			artifactValue.Tree.ManifestSHA256 != expected.manifest ||
			artifactValue.Tree.LauncherSHA256 != expected.launcher ||
			artifactValue.Tree.Usage != expected.usage {
			t.Fatalf("lock %q identity = %+v / %+v, want %+v", key, entry, artifactValue, expected)
		}
	}

	debug := environment.Lock.Versions["java-debug-server"]
	if !strings.Contains(debug.Artifacts["linux-arm64"].URL, "/0.59.0/") {
		t.Fatal("java debug carrier VSIX 0.59.0 is not recorded separately from server 0.53.2")
	}
	dotnet := environment.Lock.Versions["dotnet-sdk"]
	if dotnet.Version != "10.0.400" ||
		dotnet.Artifacts["linux-arm64"].SHA256 != "13c219bfd1ff00a886c1523a9c7027c4f24c1e730e653376d2b81f1435da5a59" {
		t.Fatalf("dotnet SDK identity = %+v", dotnet)
	}
	kotlinTree := environment.Lock.Versions["kotlin-language-server"].Artifacts["linux-arm64"].Tree
	if kotlinTree == nil ||
		!slices.Contains(kotlinTree.RequiredPaths, "extension/server/jbr/lib/jspawnhelper") ||
		!slices.Contains(kotlinTree.ExecutablePaths, "extension/server/jbr/lib/jspawnhelper") {
		t.Fatal("Kotlin runtime tree does not preserve the bundled JBR jspawnhelper executable")
	}
	html := environment.Lock.Versions["vscode-html-language-server"]
	if html.Version != "4.10.0" || html.NPM == nil ||
		html.NPM.SHA256 != "d6e2d090d09c4b91daa74e9e7462a3d3f244efb96aa5111004cfffa49d6dc9ef" {
		t.Fatalf("HTML language server identity = %+v", html)
	}
	epoch := environment.Lock.CompatibilityEpochs["neovim-razor-2026-08"]
	wantEpoch := []string{"neovim", "nvchad", "nvim-dotnet", "roslyn-language-server", "vscode-html-language-server"}
	if !reflect.DeepEqual(epoch.Members, wantEpoch) {
		t.Fatalf("Neovim/Razor epoch = %v, want %v", epoch.Members, wantEpoch)
	}
	for _, id := range []string{"nvim-jvm", "nvim-dotnet"} {
		component := catalogComponentByID(t, environment, id)
		if component.Targets[catalog.TargetLimaGuest].Status != catalog.StatusSupported {
			t.Fatalf("%s is not Lima-supported", id)
		}
	}
	for _, id := range []string{
		"java-debug-server", "java-test-server", "jdt-language-server",
		"kotlin-debug-adapter", "kotlin-language-server", "netcoredbg",
		"roslyn-language-server", "rust-analyzer", "spring-tools-language-server",
	} {
		component := catalogComponentByID(t, environment, id)
		if got := component.Targets[catalog.TargetLimaGuest].Package; got != id {
			t.Fatalf("%s Lima package = %q, want exact component package", id, got)
		}
	}
}

func TestLimaIDEProfilesComposeExactIndependentSlices(t *testing.T) {
	environment := loadCatalog(t)
	wantJVM := []string{
		"base-cli", "gradle", "java", "java-debug-server", "java-test-server",
		"jdt-language-server", "kotlin", "kotlin-debug-adapter", "kotlin-language-server", "mise", "neovim",
		"nvchad", "nvim-jvm", "spring-tools-language-server",
	}
	wantDotNet := []string{
		"base-cli", "bun", "dotnet-sdk", "mise", "neovim", "netcoredbg", "nvchad",
		"nvim-dotnet", "roslyn-language-server", "vscode-html-language-server",
	}
	wantLegacy := []string{
		"base-cli", "bun", "c-toolchain", "go", "mise", "neovim", "nvchad",
		"nvim-ide-tools", "pyright", "python", "rust", "rust-analyzer",
	}
	for profile, want := range map[string][]string{
		"nvim-jvm":    wantJVM,
		"nvim-dotnet": wantDotNet,
		"nvim-ide":    wantLegacy,
	} {
		resolved, err := catalog.ResolveProfile(environment, profile, catalog.TargetLimaGuest)
		if err != nil {
			t.Fatalf("ResolveProfile(%s): %v", profile, err)
		}
		got := resolvedIDList(resolved)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s closure = %v, want %v", profile, got, want)
		}
	}
	resolved, err := catalog.ResolveProfile(environment, "nvim-full", catalog.TargetLimaGuest)
	if err != nil {
		t.Fatalf("ResolveProfile(nvim-full): %v", err)
	}
	wantFull := uniqueSorted(append(append(append([]string{}, wantJVM...), wantDotNet...), wantLegacy...))
	if got := resolvedIDList(resolved); !reflect.DeepEqual(got, wantFull) {
		t.Fatalf("nvim-full closure = %v, want exact union %v", got, wantFull)
	}

	candidates := map[string]bool{}
	for _, component := range catalog.SelectionCandidates(environment) {
		candidates[component.ID] = true
	}
	for _, internal := range []string{
		"dotnet-sdk", "jdt-language-server", "java-debug-server", "java-test-server",
		"kotlin-debug-adapter", "kotlin-language-server", "netcoredbg", "roslyn-language-server",
		"rust-analyzer", "spring-tools-language-server", "vscode-html-language-server",
	} {
		if candidates[internal] {
			t.Fatalf("dependency-only cohort member %q leaked into direct candidates", internal)
		}
	}
}

func TestRustAnalyzerArtifactsAreExactForBothGuestArchitectures(t *testing.T) {
	environment := loadCatalog(t)
	component := catalogComponentByID(t, environment, "rust-analyzer")
	if component.SelectionPolicy != catalog.SelectionPolicyDependencyOnly ||
		!slices.Contains(component.Dependencies, "rust") {
		t.Fatalf("rust-analyzer component contract = %+v", component)
	}
	if len(component.Verification.Functional) != 0 {
		t.Fatalf("rust-analyzer repeats its version probe as a functional check: %v", component.Verification.Functional)
	}
	entry := environment.Lock.Versions["rust-analyzer"]
	if entry.Version != "0.3.2989-standalone" ||
		entry.Provenance != "https://github.com/rust-lang/rust-analyzer/releases/tag/2026-07-27" {
		t.Fatalf("rust-analyzer release identity = %+v", entry)
	}
	want := map[string]struct {
		url    string
		sha256 string
	}{
		"linux-arm64": {
			"https://github.com/rust-lang/rust-analyzer/releases/download/2026-07-27/rust-analyzer-linux-arm64.vsix",
			"5b766752f0b5b7cd935d012f8a3c1cc562b7be16141dc152d2f0f491787cbeb1",
		},
		"linux-amd64": {
			"https://github.com/rust-lang/rust-analyzer/releases/download/2026-07-27/rust-analyzer-linux-x64.vsix",
			"d378de9ee2f8a3034bf659e6edcd8695208e530a8fb5d0d4af6d6dbde72a8d3c",
		},
	}
	for platform, expected := range want {
		artifactValue, exists := entry.Artifacts[platform]
		if !exists || artifactValue.URL != expected.url || artifactValue.SHA256 != expected.sha256 ||
			artifactValue.Format != "zip" || artifactValue.Executable != "extension/server/rust-analyzer" {
			t.Fatalf("rust-analyzer %s artifact = %+v, want %+v", platform, artifactValue, expected)
		}
	}
}

func TestLimaVendorCohortRejectsMissingArm64Artifact(t *testing.T) {
	environment := loadCatalog(t)
	entry := environment.Lock.Versions["roslyn-language-server"]
	entry.Artifacts = map[string]catalog.Artifact{}
	environment.Lock.Versions["roslyn-language-server"] = entry
	assertValidationError(
		t,
		environment,
		`lock key "roslyn-language-server" Lima vendor component requires a reviewed linux-arm64 artifact`,
	)
}

func TestLimaEditorActionsBindOneNormalizedSliceIdentity(t *testing.T) {
	environment := loadCatalog(t)
	facts := certificationFacts(t, target.KindLimaGuest, "mds", "linux", "arm64")
	for profileID, want := range map[string]string{
		"nvim-ide": "legacy", "nvim-jvm": "jvm", "nvim-dotnet": "dotnet",
		"nvim-full": "dotnet,jvm,legacy",
	} {
		selection, err := planning.Profile(profileID)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := planning.Build(environment, facts, selection)
		if err != nil {
			t.Fatalf("Build(%s): %v", profileID, err)
		}
		count := 0
		for _, action := range plan.Actions {
			switch action.ComponentID {
			case "nvchad", "nvim-ide-tools", "nvim-jvm", "nvim-dotnet":
				count++
				if got := action.Inputs[planning.EditorSlicesInput]; got != want {
					t.Fatalf("%s action %s slices = %q, want %q", profileID, action.ComponentID, got, want)
				}
				for _, componentID := range requiredRuntimeTreesForSlices(want) {
					key := planning.RuntimeTreeInputPrefix + componentID
					var identity struct {
						ComponentID    string `json:"component_id"`
						ArchiveSHA256  string `json:"archive_sha256"`
						ManifestSHA256 string `json:"manifest_sha256"`
						LauncherSHA256 string `json:"launcher_sha256"`
					}
					if err := json.Unmarshal([]byte(action.Inputs[key]), &identity); err != nil ||
						identity.ComponentID != componentID {
						t.Fatalf("%s action %s runtime identity %s = %+v, %v", profileID, action.ComponentID, key, identity, err)
					}
					entry := environment.Lock.Versions[componentID]
					artifact := entry.Artifacts["linux-arm64"]
					if artifact.Tree == nil || identity.ArchiveSHA256 != artifact.SHA256 ||
						identity.ManifestSHA256 != artifact.Tree.ManifestSHA256 ||
						identity.LauncherSHA256 != artifact.Tree.LauncherSHA256 {
						t.Fatalf("%s action %s runtime identity %s does not match production lock", profileID, action.ComponentID, key)
					}
				}
			}
		}
		if count < 2 {
			t.Fatalf("%s bound only %d editor actions", profileID, count)
		}
	}
}

func requiredRuntimeTreesForSlices(slices string) []string {
	var result []string
	if strings.Contains(slices, "jvm") {
		result = append(result, "java-debug-server", "java-test-server", "jdt-language-server", "kotlin-debug-adapter", "kotlin-language-server", "spring-tools-language-server")
	}
	if strings.Contains(slices, "dotnet") {
		result = append(result, "netcoredbg", "roslyn-language-server")
	}
	sort.Strings(result)
	return result
}

func TestRuntimeTreeResourceUsageAndExtractionBoundsAreStrict(t *testing.T) {
	environment := validEnvironment()
	entry := environment.Lock.Versions["fixture"]
	artifactValue := catalog.Artifact{
		URL: "https://example.com/fixture.zip", SHA256: strings.Repeat("c", 64),
		Format: "zip", Executable: "bin/fixture",
	}
	artifactValue.Tree = &catalog.RuntimeTreeIdentity{
		ManifestSHA256: strings.Repeat("a", 64),
		LauncherSHA256: strings.Repeat("b", 64),
		RequiredPaths:  []string{artifactValue.Executable},
		Usage:          "floating",
	}
	entry.Artifacts = map[string]catalog.Artifact{"linux-arm64": artifactValue}
	environment.Lock.Versions["fixture"] = entry
	assertValidationError(t, environment, `runtime tree usage "floating"`)

	artifactValue.Tree.Usage = "resource"
	artifactValue.Tree.MaxTotalBytes = 2 << 30
	entry.Artifacts["linux-arm64"] = artifactValue
	environment.Lock.Versions["fixture"] = entry
	assertValidationError(t, environment, "must declare both max_total_bytes and max_entries")
}

func TestCatalogVerificationCommandsStayInsideNonPrivilegedProbeAllowlist(
	t *testing.T,
) {
	environment := loadCatalog(t)
	for _, component := range environment.Catalog.Components {
		for _, argv := range [][]string{
			component.Verification.Command,
			component.Verification.Functional,
		} {
			if len(argv) == 0 {
				continue
			}
			err := packages.ValidateCatalogVerificationCommand(
				component.ID,
				transport.Command{
					Executable: argv[0],
					Arguments:  argv[1:],
				},
			)
			if err != nil {
				t.Fatalf(
					"component %s verification %v is not safe: %v",
					component.ID,
					argv,
					err,
				)
			}
		}
	}
}

func TestCatalogRevisionBindsGuestImageIdentity(t *testing.T) {
	environment := loadCatalog(t)
	before, err := catalog.Revision(environment)
	if err != nil {
		t.Fatalf("Revision(before): %v", err)
	}
	specification := environment.Targets["ubuntu-26.04"]
	specification.WSLImages = map[string]catalog.ImageSpec{
		"amd64": specification.WSLImages["amd64"],
		"arm64": specification.WSLImages["arm64"],
	}
	arm64 := specification.WSLImages["arm64"]
	arm64.SHA256 = strings.Repeat("0", 64)
	specification.WSLImages["arm64"] = arm64
	environment.Targets = map[string]catalog.TargetSpec{
		"ubuntu-26.04": specification,
	}
	after, err := catalog.Revision(environment)
	if err != nil {
		t.Fatalf("Revision(after): %v", err)
	}
	if before == after {
		t.Fatal("catalog revision did not bind changed WSL image digest")
	}
}

func TestCatalogRevisionBindsExactMiseInputs(t *testing.T) {
	environment := loadCatalog(t)
	before, err := catalog.Revision(environment)
	if err != nil {
		t.Fatalf("Revision(before): %v", err)
	}
	environment.Mise.Lock += "\n# reviewed identity change\n"
	after, err := catalog.Revision(environment)
	if err != nil {
		t.Fatalf("Revision(after): %v", err)
	}
	if before == after {
		t.Fatal("catalog revision did not bind mise.lock bytes")
	}
}

func TestCatalogLoadNormalizesMiseLineEndings(t *testing.T) {
	source := filepath.Join(repositoryRoot(t), "catalog")
	fixture := t.TempDir()
	if err := os.CopyFS(fixture, os.DirFS(source)); err != nil {
		t.Fatalf("CopyFS(catalog): %v", err)
	}
	for _, name := range []string{"mise.toml", "mise.lock"} {
		path := filepath.Join(fixture, name)
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
		crlf := strings.ReplaceAll(normalized, "\n", "\r\n")
		if err := os.WriteFile(path, []byte(crlf), 0o600); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}

	lfEnvironment := loadCatalog(t)
	crlfEnvironment, err := catalog.Load(fixture)
	if err != nil {
		t.Fatalf("Load(CRLF catalog): %v", err)
	}
	if strings.Contains(crlfEnvironment.Mise.Config, "\r") ||
		strings.Contains(crlfEnvironment.Mise.Lock, "\r") {
		t.Fatal("loaded mise inputs retained checkout-specific CR characters")
	}
	lfRevision, err := catalog.Revision(lfEnvironment)
	if err != nil {
		t.Fatalf("Revision(LF): %v", err)
	}
	crlfRevision, err := catalog.Revision(crlfEnvironment)
	if err != nil {
		t.Fatalf("Revision(CRLF): %v", err)
	}
	if crlfRevision != lfRevision {
		t.Fatalf(
			"checkout line endings changed catalog revision: %s != %s",
			crlfRevision,
			lfRevision,
		)
	}
}

func TestValidationRejectsMiseLockArtifactDrift(t *testing.T) {
	environment := loadCatalog(t)
	environment.Mise.Lock = strings.Replace(
		environment.Mise.Lock,
		"sha256:fe4789e92b1f33358680864bbe8704289e7bb5fc207d80623c308935bd696d49",
		"sha256:"+strings.Repeat("0", 64),
		1,
	)
	assertValidationError(t, environment, `mise lock tool "go" platform "linux-arm64"`)
}

func TestStrictYAMLRejectsUnknownField(t *testing.T) {
	fixture := filepath.Join(repositoryRoot(t), "tests", "fixtures", "catalog", "unknown-field")
	_, err := catalog.Load(fixture)
	if err == nil || !strings.Contains(err.Error(), "field unexpected not found") {
		t.Fatalf("Load() error = %v, want strict unknown-field error", err)
	}
}

func TestValidationRejectsDuplicateCapabilityOwner(t *testing.T) {
	environment := validEnvironment()
	duplicate := environment.Catalog.Components[0]
	duplicate.ID = "duplicate"
	duplicate.VersionPolicy.LockKey = "duplicate"
	environment.Lock.Versions["duplicate"] = catalog.LockEntry{
		Version: "1.0.0", Source: "fixture", Provenance: "https://example.com/duplicate",
	}
	environment.Catalog.Components = append(environment.Catalog.Components, duplicate)

	assertValidationError(t, environment, "duplicate owners")
}

func TestValidationRejectsDependencyCycle(t *testing.T) {
	environment := validEnvironment()
	second := fixtureComponent("second", "second-capability", "second")
	environment.Lock.Versions["second"] = catalog.LockEntry{
		Version: "1.0.0", Source: "fixture", Provenance: "https://example.com/second",
	}
	environment.Catalog.Components[0].Dependencies = []string{"second"}
	second.Dependencies = []string{"fixture"}
	environment.Catalog.Components = append(environment.Catalog.Components, second)

	assertValidationError(t, environment, "dependency cycle")
}

func TestValidationRejectsTargetInstallerMismatch(t *testing.T) {
	environment := validEnvironment()
	cell := environment.Catalog.Components[0].Targets[catalog.TargetWindowsHost]
	cell.Installer = "brew"
	environment.Catalog.Components[0].Targets[catalog.TargetWindowsHost] = cell

	assertValidationError(t, environment, "cannot use installer")
}

func TestValidationRejectsCredentialLikeMaterial(t *testing.T) {
	environment := validEnvironment()
	environment.Profiles["minimal"] = catalog.Profile{
		SchemaVersion: 1,
		ID:            "minimal",
		Description:   "token=do-not-store-this",
		Selection:     []string{"fixture"},
	}

	assertValidationError(t, environment, "credential-like material")
}

func TestValidationRejectsReviewedURLQuery(t *testing.T) {
	environment := validEnvironment()
	entry := environment.Lock.Versions["fixture"]
	entry.Provenance = "https://example.com/fixture?token=secret"
	environment.Lock.Versions["fixture"] = entry

	assertValidationError(t, environment, "without a query or fragment")
}

func TestCompatibilityEpochRequiresEveryExactMember(t *testing.T) {
	environment := validEnvironment()
	environment.Lock.CompatibilityEpochs = map[string]catalog.CompatibilityEpoch{
		"editor": {Members: []string{"fixture", "missing"}},
	}
	entry := environment.Lock.Versions["fixture"]
	entry.CompatibilityEpoch = "editor"
	environment.Lock.Versions["fixture"] = entry

	assertValidationError(t, environment, `compatibility epoch "editor" references missing lock key "missing"`)
}

func TestCompatibilityEpochRejectsPartialMembership(t *testing.T) {
	environment := validEnvironment()
	environment.Lock.CompatibilityEpochs = map[string]catalog.CompatibilityEpoch{
		"editor": {Members: []string{"fixture"}},
	}

	assertValidationError(t, environment, `lock key "fixture" must declare compatibility epoch "editor"`)
}

func TestCompatibilityEpochRequiresMultipleMembers(t *testing.T) {
	environment := validEnvironment()
	environment.Lock.CompatibilityEpochs = map[string]catalog.CompatibilityEpoch{
		"editor": {Members: []string{"fixture"}},
	}
	entry := environment.Lock.Versions["fixture"]
	entry.CompatibilityEpoch = "editor"
	environment.Lock.Versions["fixture"] = entry

	assertValidationError(t, environment, `compatibility epoch "editor" requires at least two members`)
}

func TestValidationRejectsFloatingReviewedIdentity(t *testing.T) {
	for _, version := range []string{
		"latest",
		"nightly",
		"1.0.0-SNAPSHOT",
		"^1.2.3",
		"~1.2.3",
		">=1.2.3",
		"1.x",
		"1.2.3 || 2.0.0",
	} {
		t.Run(version, func(t *testing.T) {
			environment := validEnvironment()
			entry := environment.Lock.Versions["fixture"]
			entry.Version = version
			environment.Lock.Versions["fixture"] = entry
			assertValidationError(t, environment, `lock key "fixture" has floating version`)
		})
	}
}

func TestRuntimeTreeRequiresCompleteImmutableLayout(t *testing.T) {
	environment := validEnvironment()
	entry := environment.Lock.Versions["fixture"]
	entry.Artifacts = map[string]catalog.Artifact{
		"linux-arm64": {
			URL:        "https://example.com/fixture.tar.gz",
			SHA256:     strings.Repeat("a", 64),
			Format:     "tar.gz",
			Executable: "bin/fixture",
			Tree:       &catalog.RuntimeTreeIdentity{},
		},
	}
	environment.Lock.Versions["fixture"] = entry

	assertValidationError(t, environment, `runtime tree requires manifest SHA-256, launcher SHA-256, and required paths`)
}

func TestFixtureCacheRequiresDependencyGraphAndReadOnlyManifest(t *testing.T) {
	environment := validEnvironment()
	entry := environment.Lock.Versions["fixture"]
	entry.FixtureCache = &catalog.FixtureCacheIdentity{}
	environment.Lock.Versions["fixture"] = entry

	assertValidationError(t, environment, `fixture cache requires dependency graph, read-only manifest, and producer SHA-256`)
}

func TestFixtureCacheProducerDigestMustMatchReviewedArtifact(t *testing.T) {
	environment := validEnvironment()
	entry := environment.Lock.Versions["fixture"]
	entry.Artifacts = map[string]catalog.Artifact{
		"linux-arm64": {
			URL:        "https://example.com/fixture.tar.gz",
			SHA256:     strings.Repeat("a", 64),
			Format:     "tar.gz",
			Executable: "bin/fixture",
		},
	}
	entry.FixtureCache = &catalog.FixtureCacheIdentity{
		DependencyGraphSHA256:  strings.Repeat("b", 64),
		ReadOnlyManifestSHA256: strings.Repeat("c", 64),
		ProducerSHA256:         strings.Repeat("d", 64),
	}
	environment.Lock.Versions["fixture"] = entry

	assertValidationError(t, environment, `fixture cache producer SHA-256 must match a reviewed archive artifact`)
}

func TestCompatibilityEpochCanonicalOrdering(t *testing.T) {
	environment := validEnvironment()
	second := fixtureComponent("second", "second-capability", "second")
	environment.Catalog.Components = append(environment.Catalog.Components, second)
	environment.Lock.Versions["second"] = catalog.LockEntry{
		Version: "2.0.0", Source: "fixture", Provenance: "https://example.com/second",
		CompatibilityEpoch: "editor",
	}
	entry := environment.Lock.Versions["fixture"]
	entry.CompatibilityEpoch = "editor"
	environment.Lock.Versions["fixture"] = entry
	environment.Lock.CompatibilityEpochs = map[string]catalog.CompatibilityEpoch{
		"editor": {Members: []string{"fixture", "second"}},
	}
	first, err := catalog.Revision(environment)
	if err != nil {
		t.Fatalf("Revision(first): %v", err)
	}
	epoch := environment.Lock.CompatibilityEpochs["editor"]
	epoch.Members = []string{"second", "fixture"}
	environment.Lock.CompatibilityEpochs["editor"] = epoch
	secondRevision, err := catalog.Revision(environment)
	if err != nil {
		t.Fatalf("Revision(second): %v", err)
	}
	if first != secondRevision {
		t.Fatalf("epoch member order changed revision: %s != %s", first, secondRevision)
	}
}

func TestPublishedSchemasAndSemanticValidationRejectSameCoreDrift(t *testing.T) {
	for _, test := range []struct {
		name       string
		schemaPath string
		document   func(catalog.Environment) any
		mutate     func(*catalog.Environment)
	}{
		{
			name:       "pinned policy without lock key",
			schemaPath: "environment.schema.json",
			document: func(environment catalog.Environment) any {
				return environment.Catalog
			},
			mutate: func(environment *catalog.Environment) {
				environment.Catalog.Components[0].VersionPolicy.LockKey = ""
			},
		},
		{
			name:       "supported target without installer",
			schemaPath: "environment.schema.json",
			document: func(environment catalog.Environment) any {
				return environment.Catalog
			},
			mutate: func(environment *catalog.Environment) {
				cell := environment.Catalog.Components[0].Targets[catalog.TargetLimaGuest]
				cell.Installer = ""
				environment.Catalog.Components[0].Targets[catalog.TargetLimaGuest] = cell
			},
		},
		{
			name:       "query-bearing provenance",
			schemaPath: "lock.schema.json",
			document: func(environment catalog.Environment) any {
				return environment.Lock
			},
			mutate: func(environment *catalog.Environment) {
				entry := environment.Lock.Versions["fixture"]
				entry.Provenance = "https://example.com/fixture?token=secret"
				environment.Lock.Versions["fixture"] = entry
			},
		},
		{
			name:       "unknown component kind",
			schemaPath: "environment.schema.json",
			document: func(environment catalog.Environment) any {
				return environment.Catalog
			},
			mutate: func(environment *catalog.Environment) {
				environment.Catalog.Components[0].Kind = "unknown"
			},
		},
		{
			name:       "invalid component identifier",
			schemaPath: "environment.schema.json",
			document: func(environment catalog.Environment) any {
				return environment.Catalog
			},
			mutate: func(environment *catalog.Environment) {
				environment.Catalog.Components[0].ID = "Invalid_ID"
			},
		},
		{
			name:       "empty verification argument",
			schemaPath: "environment.schema.json",
			document: func(environment catalog.Environment) any {
				return environment.Catalog
			},
			mutate: func(environment *catalog.Environment) {
				environment.Catalog.Components[0].Verification.Command = []string{""}
			},
		},
		{
			name:       "invalid artifact platform identifier",
			schemaPath: "lock.schema.json",
			document: func(environment catalog.Environment) any {
				return environment.Lock
			},
			mutate: func(environment *catalog.Environment) {
				entry := environment.Lock.Versions["fixture"]
				entry.Artifacts = map[string]catalog.Artifact{
					"Linux_AMD64": {
						URL:        "https://example.com/fixture",
						SHA256:     strings.Repeat("a", 64),
						Format:     "binary",
						Executable: "fixture",
					},
				}
				environment.Lock.Versions["fixture"] = entry
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			environment := validEnvironment()
			test.mutate(&environment)
			if err := catalog.Validate(environment); err == nil {
				t.Fatal("semantic validation accepted invalid fixture")
			}
			if schemaAccepts(t, test.schemaPath, test.document(environment)) {
				t.Fatal("published JSON Schema accepted semantically invalid fixture")
			}
		})
	}
}

func TestPublishedTargetSchemaRejectsExplicitEmptyAndIncompatibleCells(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "explicit empty installer",
			mutate: func(cell map[string]any) {
				cell["installer"] = ""
			},
		},
		{
			name: "explicit empty package",
			mutate: func(cell map[string]any) {
				cell["package"] = ""
			},
		},
		{
			name: "explicit empty reason",
			mutate: func(cell map[string]any) {
				cell["status"] = "unsupported"
				delete(cell, "installer")
				delete(cell, "package")
				cell["reason"] = ""
			},
		},
		{
			name: "target incompatible installer",
			mutate: func(cell map[string]any) {
				cell["installer"] = "winget"
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			encoded, err := json.Marshal(validEnvironment().Catalog)
			if err != nil {
				t.Fatalf("Marshal(catalog): %v", err)
			}
			var document map[string]any
			if err := json.Unmarshal(encoded, &document); err != nil {
				t.Fatalf("Unmarshal(catalog): %v", err)
			}
			components := document["components"].([]any)
			component := components[0].(map[string]any)
			targets := component["targets"].(map[string]any)
			cell := targets["lima-guest"].(map[string]any)
			test.mutate(cell)
			if schemaAccepts(t, "environment.schema.json", document) {
				t.Fatal("published JSON Schema accepted invalid raw target cell")
			}
		})
	}
}

func schemaAccepts(t *testing.T, name string, document any) bool {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(
		repositoryRoot(t),
		"catalog",
		"schema",
		name,
	))
	if err != nil {
		t.Fatalf("ReadFile(schema): %v", err)
	}
	var schemaDocument any
	if err := json.Unmarshal(content, &schemaDocument); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(name, schemaDocument); err != nil {
		t.Fatalf("AddResource(): %v", err)
	}
	compiled, err := compiler.Compile(name)
	if err != nil {
		t.Fatalf("Compile(): %v", err)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("Marshal(document): %v", err)
	}
	var jsonDocument any
	if err := json.Unmarshal(encoded, &jsonDocument); err != nil {
		t.Fatalf("decode document: %v", err)
	}
	return compiled.Validate(jsonDocument) == nil
}

func TestGuestAllExcludesHostWorkspaceTools(t *testing.T) {
	environment := loadCatalog(t)
	resolved, err := catalog.ResolveProfile(environment, "all", catalog.TargetLimaGuest)
	if err != nil {
		t.Fatalf("ResolveProfile(all): %v", err)
	}
	hostOnly := map[string]bool{
		"claude-code": true,
		"codex":       true,
		"herdr":       true,
		"opencode":    true,
	}
	for _, item := range resolved {
		if item.Component.Kind == "gui" {
			t.Fatalf("guest all contains GUI component %q", item.Component.ID)
		}
		if hostOnly[item.Component.ID] {
			t.Fatalf("guest all contains host workspace tool %q", item.Component.ID)
		}
		if item.Support.Status == catalog.StatusUnsupported {
			t.Fatalf("guest all contains unsupported component %q", item.Component.ID)
		}
	}
}

func TestWorkstationProfilesKeepHostAndGuestResponsibilitiesSeparate(t *testing.T) {
	environment := loadCatalog(t)
	tests := []struct {
		profile string
		target  catalog.TargetKind
		want    []string
		forbid  []string
	}{
		{
			profile: "windows-host", target: catalog.TargetWindowsHost,
			want:   []string{"go", "c-toolchain", "python", "claude-code", "opencode", "codex", "wezterm", "herdr", "gh", "wsl"},
			forbid: []string{"neovim", "nvchad", "docker-engine"},
		},
		{
			profile: "macos-host", target: catalog.TargetMacOSHost,
			want:   []string{"go", "c-toolchain", "python", "typescript", "java", "claude-code", "opencode", "codex", "wezterm", "herdr", "neovim", "nvchad", "gh", "docker-engine", "lima"},
			forbid: []string{"wsl"},
		},
		{
			profile: "wsl-guest", target: catalog.TargetWSLGuest,
			want: []string{
				"go", "c-toolchain", "rust", "xmake", "python", "uv", "neovim", "nvchad", "nvim-ide-tools",
				"rust-analyzer",
				"java", "kotlin", "gradle", "nvim-jvm", "dotnet-sdk", "nvim-dotnet",
				"gh", "docker-engine",
			},
			forbid: []string{"claude-code", "opencode", "codex", "herdr", "typescript", "flutter"},
		},
		{
			profile: "lima-guest", target: catalog.TargetLimaGuest,
			want: []string{
				"go", "c-toolchain", "rust", "xmake", "python", "uv", "neovim", "nvchad", "nvim-ide-tools",
				"rust-analyzer",
				"java", "kotlin", "gradle", "nvim-jvm", "dotnet-sdk", "nvim-dotnet",
				"gh", "docker-engine",
			},
			forbid: []string{"claude-code", "opencode", "codex", "herdr", "typescript", "flutter"},
		},
	}

	for _, test := range tests {
		t.Run(test.profile, func(t *testing.T) {
			resolved, err := catalog.ResolveProfile(environment, test.profile, test.target)
			if err != nil {
				t.Fatalf("ResolveProfile(): %v", err)
			}
			ids := resolvedIDs(resolved)
			for _, id := range test.want {
				if !ids[id] {
					t.Fatalf("%s is missing %q: %v", test.profile, id, ids)
				}
			}
			for _, id := range test.forbid {
				if ids[id] {
					t.Fatalf("%s unexpectedly includes %q: %v", test.profile, id, ids)
				}
			}
			for _, item := range resolved {
				if item.Support.Status == catalog.StatusUnsupported {
					t.Fatalf("%s selected unsupported component %q", test.profile, item.Component.ID)
				}
			}
		})
	}
}

func TestWSLGuestVendorComponentsHaveAMD64Artifacts(t *testing.T) {
	environment := loadCatalog(t)
	resolved, err := catalog.ResolveProfile(environment, "wsl-guest", catalog.TargetWSLGuest)
	if err != nil {
		t.Fatalf("ResolveProfile(wsl-guest): %v", err)
	}

	for _, item := range resolved {
		if item.Support.Installer != "vendor" {
			continue
		}
		lock, ok := environment.Lock.Versions[item.Component.VersionPolicy.LockKey]
		if !ok {
			t.Fatalf("%s lock %q is missing", item.Component.ID, item.Component.VersionPolicy.LockKey)
		}
		if _, ok := lock.Artifacts["linux-amd64"]; !ok {
			t.Fatalf("%s has no linux-amd64 artifact", item.Component.ID)
		}
	}
}

func TestNvimIDEProfileResolvesOnlyReviewedGuestToolGraph(t *testing.T) {
	environment := loadCatalog(t)
	resolved, err := catalog.ResolveProfile(
		environment,
		"nvim-ide",
		catalog.TargetWSLGuest,
	)
	if err != nil {
		t.Fatalf("ResolveProfile(nvim-ide): %v", err)
	}
	want := map[string]bool{
		"base-cli":       true,
		"bun":            true,
		"c-toolchain":    true,
		"go":             true,
		"mise":           true,
		"neovim":         true,
		"nvchad":         true,
		"nvim-ide-tools": true,
		"pyright":        true,
		"python":         true,
		"rust":           true,
		"rust-analyzer":  true,
	}
	if got := resolvedIDs(resolved); !reflect.DeepEqual(got, want) {
		t.Fatalf("nvim-ide resolved ids = %v, want exactly %v", got, want)
	}
	for _, item := range resolved {
		if item.Component.Kind == "agent" || item.Component.Kind == "gui" {
			t.Fatalf("nvim-ide resolved forbidden %s component %q", item.Component.Kind, item.Component.ID)
		}
	}
}

func TestCertificationProfilesAreReachableOnExactTargets(t *testing.T) {
	environment := loadCatalog(t)
	tests := []struct {
		profile string
		facts   target.Facts
	}{
		{
			profile: "certification-macos-host",
			facts: certificationFacts(
				t,
				target.KindMacOSHost,
				"local",
				"darwin",
				"arm64",
			),
		},
		{
			profile: "certification-windows-host",
			facts: certificationFacts(
				t,
				target.KindWindowsHost,
				"local",
				"windows",
				"amd64",
			),
		},
		{
			profile: "certification-wsl-guest",
			facts: certificationFacts(
				t,
				target.KindWSLGuest,
				"Ubuntu-26.04",
				"linux",
				"amd64",
			),
		},
		{
			profile: "certification-lima-guest",
			facts: certificationFacts(
				t,
				target.KindLimaGuest,
				"mds",
				"linux",
				"arm64",
			),
		},
	}

	coveredKinds := make(map[string]bool)
	coveredComponents := make(map[string]bool)
	for _, test := range tests {
		t.Run(test.profile, func(t *testing.T) {
			selection, err := planning.Profile(test.profile)
			if err != nil {
				t.Fatalf("Profile(): %v", err)
			}
			plan, err := planning.Build(environment, test.facts, selection)
			if err != nil {
				t.Fatalf("Build(): %v", err)
			}
			if len(plan.Actions) == 0 {
				t.Fatal("certification profile resolved no actions")
			}
			if len(plan.Blockers) != 0 {
				t.Fatalf(
					"certification profile has static blockers: %+v",
					plan.Blockers,
				)
			}
			for _, action := range plan.Actions {
				component := catalogComponentByID(
					t,
					environment,
					action.ComponentID,
				)
				coveredKinds[component.Kind] = true
				coveredComponents[component.ID] = true
			}
		})
	}

	for _, kind := range []string{
		"agent",
		"cli",
		"container",
		"editor",
		"language",
		"platform",
	} {
		if !coveredKinds[kind] {
			t.Fatalf(
				"certification profiles do not cover representative %q surface",
				kind,
			)
		}
	}
	for _, component := range []string{
		"base-cli",
		"codex",
		"docker-engine",
		"go",
		"lima",
		"neovim",
		"wsl",
	} {
		if !coveredComponents[component] {
			t.Fatalf(
				"certification profiles do not cover representative component %q",
				component,
			)
		}
	}
}

func TestCertificationProfilesPreserveAllAndOwnerXcodeTruthfulness(t *testing.T) {
	environment := loadCatalog(t)
	facts := certificationFacts(
		t,
		target.KindMacOSHost,
		"local",
		"darwin",
		"arm64",
	)
	for _, selection := range []struct {
		name      string
		selection planning.Selection
	}{
		{name: "all", selection: planning.All()},
		{
			name: "owner",
			selection: func() planning.Selection {
				value, err := planning.Profile("owner")
				if err != nil {
					t.Fatalf("Profile(owner): %v", err)
				}
				return value
			}(),
		},
	} {
		t.Run(selection.name, func(t *testing.T) {
			plan, err := planning.Build(
				environment,
				facts,
				selection.selection,
			)
			if err != nil {
				t.Fatalf("Build(): %v", err)
			}
			for _, blocker := range plan.Blockers {
				if blocker.ActionID == "macos-host:local/xcode" &&
					blocker.Status == planning.ActionActionRequired {
					return
				}
			}
			t.Fatalf(
				"%s no longer preserves the Xcode action-required blocker: %+v",
				selection.name,
				plan.Blockers,
			)
		})
	}

	certification, err := planning.Profile("certification-macos-host")
	if err != nil {
		t.Fatalf("Profile(certification-macos-host): %v", err)
	}
	plan, err := planning.Build(environment, facts, certification)
	if err != nil {
		t.Fatalf("Build(certification-macos-host): %v", err)
	}
	if len(plan.Blockers) != 0 {
		t.Fatalf(
			"certification macOS profile has static blockers: %+v",
			plan.Blockers,
		)
	}
	for _, action := range plan.Actions {
		if action.ComponentID == "xcode" {
			t.Fatal("certification macOS profile selected manual Xcode")
		}
	}
}

func TestCertificationProfilesCoverEveryAutomatableComponent(t *testing.T) {
	environment := loadCatalog(t)
	for _, test := range []struct {
		profile string
		facts   target.Facts
	}{
		{
			profile: "certification-macos-host",
			facts: certificationFacts(
				t,
				target.KindMacOSHost,
				"local",
				"darwin",
				"arm64",
			),
		},
		{
			profile: "certification-windows-host",
			facts: certificationFacts(
				t,
				target.KindWindowsHost,
				"local",
				"windows",
				"amd64",
			),
		},
		{
			profile: "certification-wsl-guest",
			facts: certificationFacts(
				t,
				target.KindWSLGuest,
				"Ubuntu-26.04",
				"linux",
				"amd64",
			),
		},
		{
			profile: "certification-lima-guest",
			facts: certificationFacts(
				t,
				target.KindLimaGuest,
				"mds",
				"linux",
				"arm64",
			),
		},
	} {
		t.Run(test.profile, func(t *testing.T) {
			allPlan, err := planning.Build(environment, test.facts, planning.All())
			if err != nil {
				t.Fatalf("Build(all): %v", err)
			}
			profile, err := planning.Profile(test.profile)
			if err != nil {
				t.Fatalf("Profile(): %v", err)
			}
			certificationPlan, err := planning.Build(
				environment,
				test.facts,
				profile,
			)
			if err != nil {
				t.Fatalf("Build(certification): %v", err)
			}
			expected := make(map[string]bool)
			for _, action := range allPlan.Actions {
				if action.Status == planning.ActionPlanned {
					expected[action.ComponentID] = true
				}
			}
			actual := make(map[string]bool)
			for _, action := range certificationPlan.Actions {
				if action.Status != planning.ActionPlanned {
					t.Fatalf("certification action is not automatable: %+v", action)
				}
				actual[action.ComponentID] = true
			}
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf(
					"certification coverage = %v, want automatable all = %v",
					actual,
					expected,
				)
			}
		})
	}
}

func TestNewProfileCompositionNeedsNoCoreChange(t *testing.T) {
	environment := loadCatalog(t)
	environment.Profiles = copyProfiles(environment.Profiles)
	environment.Profiles["writing"] = catalog.Profile{
		SchemaVersion: 1,
		ID:            "writing",
		Description:   "Documentation-focused guest tools.",
		Selection:     []string{"gh", "neovim"},
	}
	if err := catalog.Validate(environment); err != nil {
		t.Fatalf("Validate(custom profile): %v", err)
	}
	resolved, err := catalog.ResolveProfile(environment, "writing", catalog.TargetWSLGuest)
	if err != nil {
		t.Fatalf("ResolveProfile(writing): %v", err)
	}
	ids := resolvedIDs(resolved)
	for _, id := range []string{"gh", "neovim"} {
		if !ids[id] {
			t.Fatalf("resolved ids = %v, missing %q", ids, id)
		}
	}
}

func validEnvironment() catalog.Environment {
	component := fixtureComponent("fixture", "fixture-capability", "fixture")
	return catalog.Environment{
		Catalog: catalog.Catalog{
			SchemaVersion: 1,
			Components:    []catalog.Component{component},
		},
		Profiles: map[string]catalog.Profile{
			"minimal": {
				SchemaVersion: 1,
				ID:            "minimal",
				Description:   "Fixture profile.",
				Selection:     []string{"fixture"},
			},
		},
		Lock: catalog.VersionLock{
			SchemaVersion: 1,
			Versions: map[string]catalog.LockEntry{
				"fixture": {
					Version: "1.0.0", Source: "fixture", Provenance: "https://example.com/fixture",
				},
			},
		},
	}
}

func fixtureComponent(id, capability, lockKey string) catalog.Component {
	targets := make(map[catalog.TargetKind]catalog.TargetSupport)
	for _, target := range catalog.TargetKinds {
		installer := "script"
		if target == catalog.TargetWindowsHost {
			installer = "winget"
		}
		targets[target] = catalog.TargetSupport{
			Status: catalog.StatusSupported, Installer: installer, Package: id,
		}
	}
	return catalog.Component{
		ID:           id,
		Name:         id,
		Kind:         "cli",
		Provides:     []string{capability},
		Dependencies: []string{},
		VersionPolicy: catalog.VersionPolicy{
			Mode: "pinned", LockKey: lockKey,
		},
		Verification: catalog.Verification{Command: []string{id, "--version"}},
		Targets:      targets,
	}
}

func assertValidationError(t *testing.T, environment catalog.Environment, want string) {
	t.Helper()
	err := catalog.Validate(environment)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Validate() error = %v, want substring %q", err, want)
	}
}

func loadCatalog(t *testing.T) catalog.Environment {
	t.Helper()
	environment, err := catalog.Load(filepath.Join(repositoryRoot(t), "catalog"))
	if err != nil {
		t.Fatalf("Load(catalog): %v", err)
	}
	return environment
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test file path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func copyProfiles(source map[string]catalog.Profile) map[string]catalog.Profile {
	copy := make(map[string]catalog.Profile, len(source))
	for id, profile := range source {
		copy[id] = profile
	}
	return copy
}

func resolvedIDs(resolved []catalog.ResolvedComponent) map[string]bool {
	ids := make(map[string]bool, len(resolved))
	for _, item := range resolved {
		ids[item.Component.ID] = true
	}
	return ids
}

func resolvedIDList(resolved []catalog.ResolvedComponent) []string {
	ids := make([]string, 0, len(resolved))
	for _, item := range resolved {
		ids = append(ids, item.Component.ID)
	}
	sort.Strings(ids)
	return ids
}

func uniqueSorted(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return slices.Compact(result)
}

func certificationFacts(
	t *testing.T,
	kind target.Kind,
	name,
	osName,
	architecture string,
) target.Facts {
	t.Helper()
	id, err := target.NewID(kind, name)
	if err != nil {
		t.Fatalf("NewID(): %v", err)
	}
	return target.Facts{
		ID:            id,
		OS:            osName,
		OSVersion:     "fixture",
		Architecture:  architecture,
		ImageRevision: "sha256:fixture",
		SystemdSupported: kind == target.KindWSLGuest ||
			kind == target.KindLimaGuest,
		SystemdActive: kind == target.KindWSLGuest ||
			kind == target.KindLimaGuest,
		Reachable: true,
	}
}

func catalogComponentByID(
	t *testing.T,
	environment catalog.Environment,
	id string,
) catalog.Component {
	t.Helper()
	for _, component := range environment.Catalog.Components {
		if component.ID == id {
			return component
		}
	}
	t.Fatalf("resolved unknown component %q", id)
	return catalog.Component{}
}
