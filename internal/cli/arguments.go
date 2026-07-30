package cli

import (
	"errors"

	"github.com/zzanghyunmoo/my-desk-setup/internal/planning"
)

type selectionArguments struct {
	all         bool
	profile     string
	components  []string
	interactive bool
}

func (arguments selectionArguments) selection(interactive []string) (planning.Selection, error) {
	sources := 0
	if arguments.all {
		sources++
	}
	if arguments.profile != "" {
		sources++
	}
	if len(arguments.components) > 0 {
		sources++
	}
	if arguments.interactive {
		sources++
	}
	if sources != 1 {
		return planning.Selection{}, errors.New(
			"choose exactly one of --all, --profile, --component, or --interactive",
		)
	}
	switch {
	case arguments.all:
		return planning.All(), nil
	case arguments.profile != "":
		return planning.Profile(arguments.profile)
	case arguments.interactive:
		return planning.Components(interactive)
	default:
		return planning.Components(arguments.components)
	}
}
