package buildinfo

// These variables are set by the release build through -ldflags.
var (
	Version   = "0.2.0-dev.3"
	Commit    = "unknown"
	BuildDate = "unknown"
)
