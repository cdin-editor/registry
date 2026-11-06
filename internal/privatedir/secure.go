// Package privatedir applies platform-native private permissions to Registry
// cache trees. Unix mode bits and Windows DACLs are deliberately implemented
// separately; neither policy secures an unsafe caller-selected parent directory.
//
// Spec: REG-026, Registry Data Contracts §8 Security and trust boundary.
// https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#8-security-and-trust-boundary
// Design: https://github.com/cdin-editor/registry/blob/main/docs/security/trust-boundaries.md.
package privatedir

// SecureTree rejects links and special files, then restricts every existing
// directory and regular file below root to the current Registry security
// boundary. New descendants inherit the directory policy. It is applied to
// cache roots before they store untrusted metadata or Git objects (REG-026).
func SecureTree(root string) error {
	return secureTree(root)
}
