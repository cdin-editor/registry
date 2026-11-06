//go:build !windows

package workspace

// applyWorkspacePrivateACL is a no-op on platforms whose native permission
// model is handled through the existing POSIX mode and ownership behavior.
func applyWorkspacePrivateACL(string, bool) error {
	return nil
}
