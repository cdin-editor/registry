//go:build windows

package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/cdin-editor/registry/internal/collector"
	"golang.org/x/sys/windows"
)

const windowsFileAllAccess windows.ACCESS_MASK = 0x001F01FF

// TestREG022WindowsWorkspaceACLs verifies native DACL behavior rather than
// comparing Windows permissions to non-portable POSIX mode bits. The lock and
// workspace root have protected DACLs; child documents inherit only the
// explicit principals granted by the workspace root.
func TestREG022WindowsWorkspaceACLs(t *testing.T) {
	repository, revision := createSourceRepository(t)
	parent := t.TempDir()
	destination := filepath.Join(parent, "prepared")
	lockPath := filepath.Join(parent, ".prepared.registry.lock")
	var stagingPath string
	hooks := prepareHooks{
		afterCollect: func([]collector.Source) error {
			assertWindowsPrivateDACL(t, lockPath, false, false, true)
			stages, err := filepath.Glob(filepath.Join(parent, ".prepared.registry-stage-*"))
			if err != nil {
				return err
			}
			if len(stages) != 1 {
				t.Fatalf("staging directories = %v, want exactly one", stages)
			}
			stagingPath = stages[0]
			assertWindowsPrivateDACL(t, stagingPath, true, false, true)
			return nil
		},
	}
	prepared, err := prepare(context.Background(), successfulResolution(repository, revision), workspaceOptions(destination), hooks)
	if err != nil {
		t.Fatal(err)
	}
	if stagingPath == "" {
		t.Fatal("staging ACL was not inspected before publication")
	}
	assertWindowsPrivateDACL(t, prepared.WorkspacePath, true, false, true)
	assertWindowsPrivateDACL(t, prepared.ManifestPath, false, true, false)
	assertWindowsPrivateDACL(t, prepared.ReportPath, false, true, false)
	for _, source := range prepared.Sources {
		path := filepath.Join(prepared.WorkspacePath, filepath.FromSlash(source.WorkspacePath))
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat collected source %q: %v", path, err)
		}
		if !info.IsDir() {
			t.Fatalf("collected source root %q is not a directory", path)
		}
		assertWindowsPrivateDACL(t, path, true, true, false)
	}
}

func assertWindowsPrivateDACL(t *testing.T, path string, directory, inherited, protected bool) {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo(%q): %v", path, err)
	}
	if descriptor == nil {
		t.Fatalf("%q returned a nil security descriptor", path)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatalf("read DACL for %q: %v", path, err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatalf("read security descriptor control for %q: %v", path, err)
	}
	if got := control&windows.SE_DACL_PROTECTED != 0; got != protected {
		t.Errorf("%q protected DACL = %v, want %v", path, got, protected)
	}
	if dacl == nil || dacl.AceCount != 3 {
		t.Fatalf("%q ACE count = %v, want exactly three allow ACEs", path, dacl)
	}
	wantSIDs := map[string]bool{
		"S-1-3-4":      false, // Owner Rights
		"S-1-5-18":     false, // LocalSystem
		"S-1-5-32-544": false, // BUILTIN\\Administrators
	}
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			t.Fatalf("read ACE %d for %q: %v", index, path, err)
		}
		if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Fatalf("%q ACE %d is not an access-allowed ACE", path, index)
		}
		if ace.Mask != windowsFileAllAccess {
			t.Errorf("%q ACE %d mask = %#x, want FILE_ALL_ACCESS %#x", path, index, ace.Mask, windowsFileAllAccess)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if _, ok := wantSIDs[sid]; !ok {
			t.Errorf("%q contains unexpected DACL principal %q", path, sid)
		} else {
			wantSIDs[sid] = true
		}
		flags := ace.Header.AceFlags
		if inherited && flags&windows.INHERITED_ACE == 0 {
			t.Errorf("%q ACE %q is not marked inherited", path, sid)
		}
		if !inherited && flags&windows.INHERITED_ACE != 0 {
			t.Errorf("%q ACE %q unexpectedly inherited", path, sid)
		}
		if directory && !inherited {
			wantFlags := uint8(windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE)
			if flags&(windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE) != wantFlags {
				t.Errorf("%q directory ACE %q flags = %#x, want OI|CI", path, sid, flags)
			}
		}
		if !directory && !inherited && flags&(windows.OBJECT_INHERIT_ACE|windows.CONTAINER_INHERIT_ACE) != 0 {
			t.Errorf("%q file ACE %q unexpectedly propagates", path, sid)
		}
	}
	for sid, seen := range wantSIDs {
		if !seen {
			t.Errorf("%q is missing required DACL principal %s", path, sid)
		}
	}
}
