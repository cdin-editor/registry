package semver

import "testing"

// TestREG006SemVerPrecedence covers the SemVer 2.0.0 ordering used by REG-006
// and REG-011; build metadata is intentionally excluded from precedence.
func TestREG006SemVerPrecedence(t *testing.T) {
	ordered := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0"}
	for i := 0; i+1 < len(ordered); i++ {
		a, err := Parse(ordered[i])
		if err != nil {
			t.Fatal(err)
		}
		b, err := Parse(ordered[i+1])
		if err != nil {
			t.Fatal(err)
		}
		if ComparePrecedence(a, b) >= 0 {
			t.Fatalf("expected %s < %s", ordered[i], ordered[i+1])
		}
	}
	a, _ := Parse("1.2.3+one")
	b, _ := Parse("1.2.3+two")
	if ComparePrecedence(a, b) != 0 {
		t.Fatal("build metadata must not affect precedence")
	}
}

// TestREG006RejectsInvalidSemVer protects the numeric and identifier rules that
// schema patterns alone cannot enforce completely.
func TestREG006RejectsInvalidSemVer(t *testing.T) {
	for _, value := range []string{"", "01.2.3", "1.02.3", "1.2.03", "1.2", "1.2.3-01", "1.2.3-alpha..1", "1.2.3+", "1.2.3-α"} {
		if _, err := Parse(value); err == nil {
			t.Errorf("Parse(%q) unexpectedly succeeded", value)
		}
	}
}

// TestREG007ConstraintGrammar ensures only the exact/conjunction grammar from
// Registry Data Contracts §2 is accepted; broad package-manager syntax is out.
func TestREG007ConstraintGrammar(t *testing.T) {
	good := []string{"1.2.3", "=1.2.3", ">=1.2.0 <2.0.0", ">1.0.0 <=1.9.9"}
	for _, value := range good {
		if _, err := ParseConstraint(value); err != nil {
			t.Errorf("ParseConstraint(%q): %v", value, err)
		}
	}
	bad := []string{"^1.2.3", "~1.2.3", "1.2", "1.x", ">=1.0.0 || <2.0.0", ">=1.0.0, <2.0.0", " >=1.0.0"}
	for _, value := range bad {
		if _, err := ParseConstraint(value); err == nil {
			t.Errorf("ParseConstraint(%q) unexpectedly succeeded", value)
		}
	}
}

// TestREG007ConstraintEvaluation checks comparator boundaries against parsed
// precedence rather than lexicographical version strings.
func TestREG007ConstraintEvaluation(t *testing.T) {
	constraint, err := ParseConstraint(">=1.2.0 <2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		version string
		want    bool
	}{{"1.2.0", true}, {"1.9.9", true}, {"2.0.0", false}, {"1.1.9", false}} {
		v, err := Parse(tc.version)
		if err != nil {
			t.Fatal(err)
		}
		if got := constraint.Allows(v); got != tc.want {
			t.Errorf("%s allowed=%t, want %t", tc.version, got, tc.want)
		}
	}
}

// TestREG006SupportsUnboundedNumericIdentifiers ensures core and numeric
// prerelease identifiers are not truncated to a machine integer width.
func TestREG006SupportsUnboundedNumericIdentifiers(t *testing.T) {
	a, err := Parse("999999999999999999999999999999999999.0.0-alpha.999999999999999999999999999999999999")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse("1000000000000000000000000000000000000.0.0-alpha.1")
	if err != nil {
		t.Fatal(err)
	}
	if ComparePrecedence(a, b) >= 0 {
		t.Fatal("large core identifiers were not compared numerically")
	}
	c, _ := Parse("1.0.0-alpha.999999999999999999999999999999999999")
	d, _ := Parse("1.0.0-alpha.1000000000000000000000000000000000000")
	if ComparePrecedence(c, d) >= 0 {
		t.Fatal("large prerelease identifiers were not compared numerically")
	}
}


// TestREG006OfficialSemVerV20ParserExamples supplements the canonical precedence
// sequence with representative valid and invalid SemVer 2.0.0 parser vectors.
func TestREG006OfficialSemVerV20ParserExamples(t *testing.T) {
	valid := []string{
		"0.0.0", "1.0.0", "1.0.0-0", "1.0.0-0A", "1.0.0-A",
		"1.0.0-x.7.z.92", "1.0.0-alpha+001", "1.0.0+20130313144700",
		"1.0.0-beta+exp.sha.5114f85", "1.0.0+21AF26D3----117B344092BD",
	}
	for _, value := range valid {
		if _, err := Parse(value); err != nil { t.Errorf("valid SemVer %q rejected: %v", value, err) }
	}
	invalid := []string{
		"1", "1.2", "1.2.3.4", "v1.2.3", "=1.2.3", "01.2.3",
		"1.2.3-0123", "1.2.3-0123.0123", "1.2.3+", "1.2.3+build..metadata",
		"1.2.3-#", "1.2.3-α",
	}
	for _, value := range invalid {
		if _, err := Parse(value); err == nil { t.Errorf("invalid SemVer %q unexpectedly accepted", value) }
	}
}
