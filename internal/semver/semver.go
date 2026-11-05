// Package semver implements the exact Semantic Versioning 2.0.0 subset used by
// Registry Data Contracts 1.0.0.
//
// Spec traceability:
//   - REG-006 and REG-007: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#2-identity-and-version-rules
//   - REG-011 and REG-012: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#5-resolution-semantics
//   - Local design: https://github.com/cdin-editor/registry/blob/main/docs/implementation/resolver.md
//
// This package is deliberately independent of Catalog, CLI, Git and filesystem
// code. Keeping precedence and constraint parsing in one small value package
// prevents callers from accidentally using lexical version ordering or a wider,
// incompatible range grammar.
package semver

import (
	"fmt"
	"strings"
)

// Version is a parsed SemVer 2.0.0 value. Original preserves the supplied form
// for identity/diagnostics; Build is retained but never participates in order.
type Version struct {
	Major    string
	Minor    string
	Patch    string
	Pre      []string
	Build    []string
	Original string
}

// Parse rejects non-canonical numeric core/prerelease identifiers and malformed
// metadata. REG-006 requires actual SemVer semantics, not only schema regex
// matching; parsing here is therefore repeated at the engine boundary.
func Parse(input string) (Version, error) {
	if input == "" || strings.TrimSpace(input) != input {
		return Version{}, fmt.Errorf("semver: version is empty or has surrounding whitespace")
	}
	v := Version{Original: input}
	coreAndPre := input
	if i := strings.IndexByte(coreAndPre, '+'); i >= 0 {
		if strings.IndexByte(coreAndPre[i+1:], '+') >= 0 {
			return Version{}, fmt.Errorf("semver: multiple build metadata separators")
		}
		var err error
		v.Build, err = parseIdentifiers(coreAndPre[i+1:], false)
		if err != nil {
			return Version{}, fmt.Errorf("semver: invalid build metadata: %w", err)
		}
		coreAndPre = coreAndPre[:i]
	}
	if i := strings.IndexByte(coreAndPre, '-'); i >= 0 {
		var err error
		v.Pre, err = parseIdentifiers(coreAndPre[i+1:], true)
		if err != nil {
			return Version{}, fmt.Errorf("semver: invalid prerelease: %w", err)
		}
		coreAndPre = coreAndPre[:i]
	}
	parts := strings.Split(coreAndPre, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("semver: core must contain major.minor.patch")
	}
	values := []*string{&v.Major, &v.Minor, &v.Patch}
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return Version{}, fmt.Errorf("semver: invalid numeric core identifier %q", part)
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return Version{}, fmt.Errorf("semver: invalid numeric core identifier %q", part)
			}
		}
		// SemVer numeric identifiers have no fixed machine-word limit. Retain
		// their canonical decimal form and compare by length, then bytes.
		*values[i] = part
	}
	return v, nil
}

// parseIdentifiers validates dot-separated SemVer prerelease/build identifiers.
// Numeric prerelease identifiers cannot have leading zeroes; build identifiers
// may. REG-006 requires these distinctions to match SemVer 2.0.0 exactly.
func parseIdentifiers(value string, prerelease bool) ([]string, error) {
	if value == "" {
		return nil, fmt.Errorf("identifier list is empty")
	}
	parts := strings.Split(value, ".")
	for _, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("empty identifier")
		}
		numeric := true
		for _, r := range part {
			if !isASCIIAlphaNumeric(r) && r != '-' {
				return nil, fmt.Errorf("invalid character %q", r)
			}
			if r < '0' || r > '9' {
				numeric = false
			}
		}
		if prerelease && numeric && len(part) > 1 && part[0] == '0' {
			return nil, fmt.Errorf("numeric prerelease identifier %q has a leading zero", part)
		}
	}
	return parts, nil
}

func isASCIIAlphaNumeric(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

// ComparePrecedence compares SemVer precedence. Build metadata is intentionally
// ignored by SemVer 2.0.0; equal precedence is handled as a resolver ambiguity
// when release identities differ (REG-011).
func ComparePrecedence(a, b Version) int {
	if order := compareNumericIdentifier(a.Major, b.Major); order != 0 {
		return order
	}
	if order := compareNumericIdentifier(a.Minor, b.Minor); order != 0 {
		return order
	}
	if order := compareNumericIdentifier(a.Patch, b.Patch); order != 0 {
		return order
	}
	if len(a.Pre) == 0 && len(b.Pre) > 0 {
		return 1
	}
	if len(a.Pre) > 0 && len(b.Pre) == 0 {
		return -1
	}
	for i := 0; i < len(a.Pre) && i < len(b.Pre); i++ {
		left, right := a.Pre[i], b.Pre[i]
		leftNumeric := isDigits(left)
		rightNumeric := isDigits(right)
		switch {
		case leftNumeric && rightNumeric:
			if order := compareNumericIdentifier(left, right); order != 0 {
				return order
			}
		case leftNumeric:
			return -1
		case rightNumeric:
			return 1
		default:
			if left < right {
				return -1
			}
			if left > right {
				return 1
			}
		}
	}
	if len(a.Pre) < len(b.Pre) {
		return -1
	}
	if len(a.Pre) > len(b.Pre) {
		return 1
	}
	return 0
}

// compareNumericIdentifier compares canonical decimal identifiers without
// integer conversion, avoiding overflow for arbitrarily large SemVer numbers.
func compareNumericIdentifier(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// HasPrerelease reports whether prerelease policy applies to this version.
func (v Version) HasPrerelease() bool { return len(v.Pre) != 0 }

// Operator is one of the six comparator operators frozen by contract 1.0.0.
type Operator string

const (
	Equal        Operator = "="
	Greater      Operator = ">"
	GreaterEqual Operator = ">="
	Less         Operator = "<"
	LessEqual    Operator = "<="
)

// Comparator is one comparison in a contract 1.0.0 conjunction.
type Comparator struct {
	Operator Operator
	Version  Version
}

// Constraint represents either one exact version or a conjunction of
// comparators. The grammar intentionally rejects OR, wildcards, partial, caret,
// tilde, hyphen and comma ranges (REG-007).
type Constraint struct{ Comparators []Comparator }

// ParseConstraint parses only the range grammar in Registry Data Contracts §2.
func ParseConstraint(input string) (Constraint, error) {
	if input == "" || strings.TrimSpace(input) != input {
		return Constraint{}, fmt.Errorf("semver: constraint is empty or has surrounding whitespace")
	}
	tokens := strings.Split(input, " ")
	out := Constraint{Comparators: make([]Comparator, 0, len(tokens))}
	for _, token := range tokens {
		if token == "" {
			return Constraint{}, fmt.Errorf("semver: empty comparator in constraint")
		}
		op := Equal
		versionText := token
		for _, candidate := range []Operator{GreaterEqual, LessEqual, Equal, Greater, Less} {
			if strings.HasPrefix(token, string(candidate)) {
				op = candidate
				versionText = strings.TrimPrefix(token, string(candidate))
				break
			}
		}
		if versionText == token && strings.HasPrefix(token, "=") {
			versionText = strings.TrimPrefix(token, "=")
		}
		version, err := Parse(versionText)
		if err != nil {
			return Constraint{}, fmt.Errorf("semver: invalid constraint token %q: %w", token, err)
		}
		out.Comparators = append(out.Comparators, Comparator{Operator: op, Version: version})
	}
	return out, nil
}

// Allows evaluates every comparator. Exact versions use SemVer precedence, so
// build metadata never changes equality, as required by SemVer 2.0.0.
func (c Constraint) Allows(candidate Version) bool {
	for _, cmp := range c.Comparators {
		order := ComparePrecedence(candidate, cmp.Version)
		switch cmp.Operator {
		case Equal:
			if order != 0 {
				return false
			}
		case Greater:
			if order <= 0 {
				return false
			}
		case GreaterEqual:
			if order < 0 {
				return false
			}
		case Less:
			if order >= 0 {
				return false
			}
		case LessEqual:
			if order > 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
