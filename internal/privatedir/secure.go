// Package privatedir applies platform-native private permissions to Registry cache trees.
package privatedir

// SecureTree rejects links and special files, then restricts every existing
// directory and regular file below root to the current Registry security
// boundary. New descendants inherit the directory policy.
func SecureTree(root string) error {
	return secureTree(root)
}
