package target

import (
	"errors"
	"fmt"
	"strings"
)

type Kind string

const (
	KindMacOSHost   Kind = "macos-host"
	KindWindowsHost Kind = "windows-host"
	KindWSLGuest    Kind = "wsl-guest"
	KindLimaGuest   Kind = "lima-guest"
)

type ID struct {
	Kind Kind   `json:"kind"`
	Name string `json:"name"`
}

func NewID(kind Kind, name string) (ID, error) {
	if !knownKind(kind) {
		return ID{}, fmt.Errorf("unknown target kind %q", kind)
	}
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, ":/\\\x00") {
		return ID{}, fmt.Errorf("invalid target name %q", name)
	}
	if (kind == KindMacOSHost || kind == KindWindowsHost) && name != "local" {
		return ID{}, errors.New("host target name must be local")
	}
	return ID{Kind: kind, Name: name}, nil
}

func ParseID(value string) (ID, error) {
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		return ID{}, fmt.Errorf("invalid target id %q", value)
	}
	return NewID(Kind(parts[0]), parts[1])
}

func (id ID) String() string {
	return string(id.Kind) + ":" + id.Name
}

func (id ID) IsGuest() bool {
	return id.Kind == KindWSLGuest || id.Kind == KindLimaGuest
}

func knownKind(kind Kind) bool {
	switch kind {
	case KindMacOSHost, KindWindowsHost, KindWSLGuest, KindLimaGuest:
		return true
	default:
		return false
	}
}
