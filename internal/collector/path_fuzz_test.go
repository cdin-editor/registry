package collector

import (
	"io/fs"
	"path"
	"strings"
	"testing"
)

func FuzzValidateSourceSubdirectory(f *testing.F) {
	for _, seed := range []string{
		"",
		".",
		"src",
		"src/include",
		"../outside",
		"/etc",
		"C:/Windows",
		"C:relative",
		"src\\include",
		"src//include",
		"src/../outside",
		"src/.",
		"\\\\server\\share",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 4096 {
			t.Skip()
		}
		err := validateSourceSubdirectory(input)
		if err != nil || input == "" {
			return
		}
		if strings.Contains(input, "\\") {
			t.Fatalf("accepted a backslash path: %q", input)
		}
		if !fs.ValidPath(input) || path.Clean(input) != input {
			t.Fatalf("accepted a non-canonical relative path: %q", input)
		}
		if len(input) >= 2 && ((input[0] >= 'a' && input[0] <= 'z') || (input[0] >= 'A' && input[0] <= 'Z')) && input[1] == ':' {
			t.Fatalf("accepted a drive-prefixed path: %q", input)
		}
	})
}
