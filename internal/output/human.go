package output

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

func Human(writer io.Writer, plan planning.Plan) error {
	if _, err := fmt.Fprintf(
		writer,
		"Plan %s\nTarget: %s\nCatalog: %s\nDigest: %s\n\n",
		plan.SchemaVersion,
		plan.Target.ID.String(),
		plan.CatalogRevision,
		plan.Digest,
	); err != nil {
		return err
	}
	for _, action := range plan.Actions {
		detail := strings.TrimSpace(action.Installer + " " + action.Package)
		if detail == "" {
			detail = action.Reason
		}
		if _, err := fmt.Fprintf(
			writer,
			"- %-15s %-24s %-16s %s\n",
			action.Status,
			action.ComponentID,
			action.Version,
			detail,
		); err != nil {
			return err
		}
		keys := make([]string, 0, len(action.Inputs))
		for key := range action.Inputs {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if _, err := fmt.Fprintf(
				writer,
				"  %s=%s\n",
				key,
				action.Inputs[key],
			); err != nil {
				return err
			}
		}
	}
	if len(plan.Blockers) == 0 {
		_, err := fmt.Fprintln(writer, "\nBlockers: none")
		return err
	}
	if _, err := fmt.Fprintln(writer, "\nBlockers:"); err != nil {
		return err
	}
	for _, blocker := range plan.Blockers {
		if _, err := fmt.Fprintf(
			writer,
			"- %s: %s\n",
			blocker.ActionID,
			blocker.Reason,
		); err != nil {
			return err
		}
	}
	return nil
}
