package buildinfo

// Version is set by release builds from the exact Git tag through -ldflags.
// Untagged development builds intentionally do not claim a release version.
var (
	Version   = "development"
	Commit    = "unknown"
	BuildDate = "unknown"
)
