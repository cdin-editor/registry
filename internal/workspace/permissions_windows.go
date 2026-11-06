//go:build windows

package workspace

import (
	"fmt"

	"golang.org/x/sys/windows"
)

const (
	workspacePrivateDirectorySDDL = "D:P(A;OICI;FA;;;OW)(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
	workspacePrivateFileSDDL      = "D:P(A;;FA;;;OW)(A;;FA;;;SY)(A;;FA;;;BA)"
)

// applyWorkspacePrivateACL replaces inherited parent permissions with a
// protected DACL. Callers invoke it immediately after creating the lock or
// staging directory, before collection or document writes begin. The object
// owner, LocalSystem and local Administrators retain full control. Directory
// ACEs propagate to all workspace descendants.
func applyWorkspacePrivateACL(path string, directory bool) error {
	sddl := workspacePrivateFileSDDL
	if directory {
		sddl = workspacePrivateDirectorySDDL
	}
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("parse private workspace security descriptor: %w", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("read private workspace DACL: %w", err)
	}
	if dacl == nil {
		return fmt.Errorf("private workspace security descriptor has no DACL")
	}
	securityInformation := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, securityInformation, nil, nil, dacl, nil); err != nil {
		return fmt.Errorf("apply protected private workspace DACL to %q: %w", path, err)
	}
	return nil
}
