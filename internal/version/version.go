package version

import "fmt"

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

func String() string {
	if Version == "dev" {
		return Version
	}
	return fmt.Sprintf("%s (commit=%s, date=%s)", Version, Commit, Date)
}
