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

type Catalog struct {
	SchemaVersion int         `yaml:"schema_version" json:"schema_version"`
	Components    []Component `yaml:"components" json:"components"`
}

type Component struct {
	ID            string                       `yaml:"id" json:"id"`
	Name          string                       `yaml:"name" json:"name"`
	Kind          string                       `yaml:"kind" json:"kind"`
	Provides      []string                     `yaml:"provides" json:"provides"`
	Dependencies  []string                     `yaml:"dependencies" json:"dependencies"`
	VersionPolicy VersionPolicy                `yaml:"version_policy" json:"version_policy"`
	Verification  Verification                 `yaml:"verification" json:"verification"`
	Targets       map[TargetKind]TargetSupport `yaml:"targets" json:"targets"`
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
	Version    string `yaml:"version" json:"version"`
	Source     string `yaml:"source" json:"source"`
	Provenance string `yaml:"provenance" json:"provenance"`
}

type Environment struct {
	Catalog  Catalog            `json:"catalog"`
	Profiles map[string]Profile `json:"profiles"`
	Lock     VersionLock        `json:"lock"`
}

type ResolvedComponent struct {
	Component Component     `json:"component"`
	Target    TargetKind    `json:"target"`
	Support   TargetSupport `json:"support"`
}
