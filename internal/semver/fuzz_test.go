package semver

import "testing"

func FuzzParseVersion(f *testing.F) {
	for _, seed := range []string{
		"0.0.0",
		"1.2.3",
		"1.2.3-alpha.1+build.7",
		"01.2.3",
		"1.2",
		"1.2.3-01",
		"999999999999999999999999999999.0.0",
		"1.2.3+",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 4096 {
			t.Skip()
		}
		version, err := Parse(input)
		if err != nil {
			return
		}
		if ComparePrecedence(version, version) != 0 {
			t.Fatalf("a parsed version does not have equal self-precedence: %q", input)
		}
		if version.Original != input {
			t.Fatalf("original version = %q, want exact input %q", version.Original, input)
		}
	})
}

func FuzzParseConstraint(f *testing.F) {
	for _, seed := range []string{
		"1.2.3",
		"=1.2.3",
		">=1.0.0 <2.0.0",
		">1.0.0 <=9.0.0",
		"^1.2.3",
		"1.2.3 || 2.0.0",
		">=1.0.0  <2.0.0",
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 4096 {
			t.Skip()
		}
		constraint, err := ParseConstraint(input)
		if err != nil {
			return
		}
		for _, comparator := range constraint.Comparators {
			if !constraint.Allows(comparator.Version) && len(constraint.Comparators) == 1 && comparator.Operator == Equal {
				t.Fatalf("an exact single-version constraint rejected its own version: %q", input)
			}
		}
	})
}
