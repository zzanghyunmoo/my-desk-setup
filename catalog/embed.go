package catalogdata

import "embed"

// FS contains the reviewed environment intent shipped with the CLI.
//
//go:embed components/*.yaml locks/*.yaml profiles/*.yaml targets/*.yaml schema/*.json mise.toml mise.lock
var FS embed.FS
