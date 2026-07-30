package adapters

import "strings"

func ValidExecutableName(value string) bool {
	return value != "." &&
		value != ".." &&
		!strings.ContainsAny(value, `/\`)
}

func ShellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
