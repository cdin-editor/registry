package validation

import (
	"fmt"
	"sort"
	"strings"
)

type Kind string

const (
	ProductDefinition Kind = "product-definition"
	CatalogIndex      Kind = "catalog-index"
	ComponentRelease  Kind = "component-release"
	ResolvedManifest  Kind = "resolved-manifest"
	ResolutionReport  Kind = "resolution-report"
	UpdateDiscoveryReport Kind = "update-discovery-report"
)

var allKinds = []Kind{
	ProductDefinition,
	CatalogIndex,
	ComponentRelease,
	ResolvedManifest,
	ResolutionReport,
	UpdateDiscoveryReport,
}

func ParseKind(value string) (Kind, error) {
	kind := Kind(value)
	for _, candidate := range allKinds {
		if kind == candidate {
			return kind, nil
		}
	}
	return "", fmt.Errorf("unknown document kind %q (expected one of: %s)", value, strings.Join(kindNames(allKinds), ", "))
}

func Kinds() []Kind {
	out := append([]Kind(nil), allKinds...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func kindNames(kinds []Kind) []string {
	values := make([]string, len(kinds))
	for i, kind := range kinds {
		values[i] = string(kind)
	}
	return values
}
