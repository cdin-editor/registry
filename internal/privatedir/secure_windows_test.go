//go:build windows

package privatedir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestSecureTreeAppliesPrivateWindowsDACLs(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(nested, "cache.json")
	if err := os.WriteFile(file, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SecureTree(root); err != nil {
		t.Fatal(err)
	}

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	userSID := user.User.Sid.String()
	for _, path := range []string{root, nested, file} {
		descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatalf("read cache DACL for %s: %v", path, err)
		}
		sddl := descriptor.String()
		if !strings.Contains(sddl, "D:P") {
			t.Errorf("cache DACL for %s is not protected: %q", path, sddl)
		}
		currentUserAllowed := strings.Contains(sddl, userSID)
		// SDDL abbreviates the built-in local Administrator SID as "LA".
		if strings.HasSuffix(userSID, "-500") && strings.Contains(sddl, ";;;LA)") {
			currentUserAllowed = true
		}
		if !currentUserAllowed || !strings.Contains(sddl, "S-1-5-32-544") && !strings.Contains(sddl, ";;;BA)") || !strings.Contains(sddl, "S-1-5-18") && !strings.Contains(sddl, ";;;SY)") {
			t.Errorf("cache DACL for %s omits the current user, Administrators or Local System: %q", path, sddl)
		}
		if strings.Contains(sddl, ";;;WD)") || strings.Contains(sddl, ";;;AU)") {
			t.Errorf("cache DACL for %s grants access to World or Authenticated Users: %q", path, sddl)
		}
	}
}
