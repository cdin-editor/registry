package platform

import "strings"

// NormalizeTargetAliases maps common host/packaging aliases to Registry's
// canonical OS, architecture and ABI identifiers. It does not infer ABI
// compatibility: the ABI remains a publisher/consumer supplied exact value.
func NormalizeTargetAliases(osName, architecture, abi string) (string, string, string) {
	osName = strings.ToLower(strings.TrimSpace(osName))
	switch osName {
	case "macos", "macosx", "osx", "mac":
		osName = "darwin"
	case "win32", "win64", "win":
		osName = "windows"
	}

	architecture = strings.ToLower(strings.TrimSpace(architecture))
	switch architecture {
	case "amd64", "x64", "x86-64":
		architecture = "x86_64"
	case "aarch64":
		architecture = "arm64"
	}

	abi = strings.ToLower(strings.TrimSpace(abi))
	return osName, architecture, abi
}
