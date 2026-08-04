package catalog

type TargetKind string

const (
	TargetMacOSHost   TargetKind = "macos-host"
	TargetWindowsHost TargetKind = "windows-host"
	TargetWSLGuest    TargetKind = "wsl-guest"
	TargetLimaGuest   TargetKind = "lima-guest"
)

var TargetKinds = []TargetKind{
	TargetMacOSHost,
	TargetWindowsHost,
	TargetWSLGuest,
	TargetLimaGuest,
}

type SupportStatus string

const (
	StatusSupported      SupportStatus = "supported"
	StatusUnsupported    SupportStatus = "unsupported"
	StatusActionRequired SupportStatus = "action-required"
)

type SelectionPolicy string

const (
	// SelectionPolicyDirect is the default for existing catalog components.
	// The empty serialized value is intentionally equivalent so older catalogs
	// remain valid without exposing a new required field.
	SelectionPolicyDirect         SelectionPolicy = "direct"
	SelectionPolicyDependencyOnly SelectionPolicy = "dependency-only"
)

type Catalog struct {
	SchemaVersion int         `yaml:"schema_version" json:"schema_version"`
	Components    []Component `yaml:"components" json:"components"`
}

type Component struct {
	ID              string                       `yaml:"id" json:"id"`
	Name            string                       `yaml:"name" json:"name"`
	Kind            string                       `yaml:"kind" json:"kind"`
	SelectionPolicy SelectionPolicy              `yaml:"selection_policy,omitempty" json:"selection_policy,omitempty"`
	Provides        []string                     `yaml:"provides" json:"provides"`
	Dependencies    []string                     `yaml:"dependencies" json:"dependencies"`
	VersionPolicy   VersionPolicy                `yaml:"version_policy" json:"version_policy"`
	Verification    Verification                 `yaml:"verification" json:"verification"`
	Targets         map[TargetKind]TargetSupport `yaml:"targets" json:"targets"`
}

type VersionPolicy struct {
	Mode    string `yaml:"mode" json:"mode"`
	LockKey string `yaml:"lock_key,omitempty" json:"lock_key,omitempty"`
}

type Verification struct {
	Command    []string `yaml:"command" json:"command"`
	Functional []string `yaml:"functional,omitempty" json:"functional,omitempty"`
}

type TargetSupport struct {
	Status    SupportStatus `yaml:"status" json:"status"`
	Installer string        `yaml:"installer,omitempty" json:"installer,omitempty"`
	Package   string        `yaml:"package,omitempty" json:"package,omitempty"`
	Reason    string        `yaml:"reason,omitempty" json:"reason,omitempty"`
}

type Profile struct {
	SchemaVersion int      `yaml:"schema_version" json:"schema_version"`
	ID            string   `yaml:"id" json:"id"`
	Description   string   `yaml:"description" json:"description"`
	Selection     []string `yaml:"selection" json:"selection"`
}

type VersionLock struct {
	SchemaVersion int                  `yaml:"schema_version" json:"schema_version"`
	Versions      map[string]LockEntry `yaml:"versions" json:"versions"`
}

type LockEntry struct {
	Version              string              `yaml:"version" json:"version"`
	Source               string              `yaml:"source" json:"source"`
	Provenance           string              `yaml:"provenance" json:"provenance"`
	InstallRef           string              `yaml:"install_ref,omitempty" json:"install_ref,omitempty"`
	NPM                  *NPMArtifact        `yaml:"npm,omitempty" json:"npm,omitempty"`
	Artifacts            map[string]Artifact `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`
	UnavailablePlatforms map[string]string   `yaml:"unavailable_platforms,omitempty" json:"unavailable_platforms,omitempty"`
}

type NPMArtifact struct {
	Tarball   string `yaml:"tarball" json:"tarball"`
	Integrity string `yaml:"integrity" json:"integrity"`
	SHA256    string `yaml:"sha256" json:"sha256"`
}

type Artifact struct {
	URL              string `yaml:"url" json:"url"`
	SHA256           string `yaml:"sha256" json:"sha256"`
	Format           string `yaml:"format" json:"format"`
	Executable       string `yaml:"executable" json:"executable"`
	ExecutableSHA256 string `yaml:"executable_sha256,omitempty" json:"executable_sha256,omitempty"`
}

type Environment struct {
	Catalog  Catalog               `json:"catalog"`
	Profiles map[string]Profile    `json:"profiles"`
	Targets  map[string]TargetSpec `json:"targets"`
	Lock     VersionLock           `json:"lock"`
	Mise     MiseFiles             `json:"mise"`
}

// MiseFiles are the exact, LF-normalized strict-mode inputs consumed by
// `mise install --locked`. Keeping them in Environment binds those installer
// inputs to the canonical catalog revision and every plan digest derived from
// it without making checkout line-ending policy part of the identity.
type MiseFiles struct {
	Config string `json:"config"`
	Lock   string `json:"lock"`
}

type TargetSpec struct {
	SchemaVersion   int                  `yaml:"schema_version" json:"schema_version"`
	ID              string               `yaml:"id" json:"id"`
	Distribution    string               `yaml:"distribution" json:"distribution"`
	Release         string               `yaml:"release" json:"release"`
	SystemdRequired bool                 `yaml:"systemd_required" json:"systemd_required"`
	WSLDistribution string               `yaml:"wsl_distribution" json:"wsl_distribution"`
	Images          map[string]ImageSpec `yaml:"images" json:"images"`
	WSLImages       map[string]ImageSpec `yaml:"wsl_images" json:"wsl_images"`
}

type ImageSpec struct {
	URL    string `yaml:"url" json:"url"`
	SHA256 string `yaml:"sha256" json:"sha256"`
}

type ResolvedComponent struct {
	Component Component     `json:"component"`
	Target    TargetKind    `json:"target"`
	Support   TargetSupport `json:"support"`
}
