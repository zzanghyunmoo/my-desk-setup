package version

import "fmt"

var (
	Version               = "dev"
	Commit                = "none"
	Date                  = "unknown"
	GuestLinuxAMD64URL    = ""
	GuestLinuxAMD64SHA256 = ""
	GuestLinuxARM64URL    = ""
	GuestLinuxARM64SHA256 = ""
)

type Artifact struct {
	URL    string
	SHA256 string
}

func String() string {
	if Version == "dev" {
		return Version
	}
	return fmt.Sprintf("%s (commit=%s, date=%s)", Version, Commit, Date)
}

func GuestLinuxArtifact(architecture string) (Artifact, bool) {
	var artifact Artifact
	switch architecture {
	case "amd64":
		artifact = Artifact{
			URL: GuestLinuxAMD64URL, SHA256: GuestLinuxAMD64SHA256,
		}
	case "arm64":
		artifact = Artifact{
			URL: GuestLinuxARM64URL, SHA256: GuestLinuxARM64SHA256,
		}
	default:
		return Artifact{}, false
	}
	return artifact, artifact.URL != "" && artifact.SHA256 != ""
}
