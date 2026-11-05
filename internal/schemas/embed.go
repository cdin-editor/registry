package schemas

import "embed"

// FS contains the Registry schemas shipped with the CLI.
//
//go:embed registry/v1/*.json
var FS embed.FS
