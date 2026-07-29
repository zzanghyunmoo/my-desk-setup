package execution

import (
	"sort"

	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

func blockedDependency(
	action planning.Action,
	statuses map[string]string,
) string {
	dependencies := append([]string(nil), action.Dependencies...)
	sort.Strings(dependencies)
	for _, dependency := range dependencies {
		status := statuses[dependency]
		if status != "ready" {
			return dependency
		}
	}
	return ""
}
