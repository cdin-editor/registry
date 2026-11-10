package buildinfo

// These variables are set by the release build through -ldflags.
var (
	Version   = "0.1.0-dev.2"
	Commit    = "unknown"
	BuildDate = "unknown"
)
