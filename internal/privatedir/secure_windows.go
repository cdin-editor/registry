//go:build windows

package privatedir

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/windows"
)

func secureTree(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("privatedir: inspect cache root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("privatedir: cache root is not a real directory")
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("privatedir: cache tree contains a reparse point or symlink")
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("privatedir: cache tree contains a special file")
		}
		return setPrivateDACL(path, info.IsDir())
	})
}

func setPrivateDACL(path string, directory bool) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("privatedir: identify current Windows user: %w", err)
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("privatedir: resolve Windows administrators SID: %w", err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("privatedir: resolve Windows Local System SID: %w", err)
	}

	var pinner runtime.Pinner
	defer pinner.Unpin()
	pinner.Pin(user.User.Sid)
	pinner.Pin(administrators)
	pinner.Pin(system)

	inheritance := uint32(windows.NO_INHERITANCE)
	if directory {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	entries := []windows.EXPLICIT_ACCESS{
		privateAccess(user.User.Sid, windows.TRUSTEE_IS_USER, inheritance),
		privateAccess(administrators, windows.TRUSTEE_IS_WELL_KNOWN_GROUP, inheritance),
		privateAccess(system, windows.TRUSTEE_IS_USER, inheritance),
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return fmt.Errorf("privatedir: build private Windows ACL: %w", err)
	}
	information := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, information, nil, nil, acl, nil); err != nil {
		return fmt.Errorf("privatedir: apply private Windows ACL: %w", err)
	}
	return nil
}

func privateAccess(sid *windows.SID, trusteeType windows.TRUSTEE_TYPE, inheritance uint32) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  trusteeType,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}
