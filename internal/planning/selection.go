package planning

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type SelectionMode string

const (
	SelectionAll        SelectionMode = "all"
	SelectionProfile    SelectionMode = "profile"
	SelectionComponents SelectionMode = "components"
)

type Selection struct {
	Mode       SelectionMode
	Profile    string
	Components []string
}

func All() Selection {
	return Selection{Mode: SelectionAll}
}

func Profile(id string) (Selection, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Selection{}, errors.New("profile id is required")
	}
	if id == "all" {
		return Selection{}, errors.New(`use all selection instead of profile "all"`)
	}
	return Selection{Mode: SelectionProfile, Profile: id}, nil
}

func Components(ids []string) (Selection, error) {
	if len(ids) == 0 {
		return Selection{}, errors.New("at least one component is required")
	}
	normalized := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			return Selection{}, errors.New("component id cannot be empty")
		}
		if seen[id] {
			return Selection{}, fmt.Errorf("duplicate component %q", id)
		}
		seen[id] = true
		normalized = append(normalized, id)
	}
	sort.Strings(normalized)
	return Selection{Mode: SelectionComponents, Components: normalized}, nil
}
