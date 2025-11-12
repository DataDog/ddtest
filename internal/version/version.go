package version

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// validIdentifierRegex matches valid pre-release and build metadata identifiers.
// They must contain only alphanumerics, hyphens, and dots.
var validIdentifierRegex = regexp.MustCompile(`^[a-zA-Z0-9.\-]+$`)

// numericComponentRegex matches numeric version components (digits only).
var numericComponentRegex = regexp.MustCompile(`^[0-9]+$`)

// Version represents a semantic-like version composed of dot-separated integer components.
// It also supports pre-release identifiers (after '-') and build metadata (after '+').
type Version struct {
	raw        string
	components []int
	preRelease string
	buildMeta  string
}

// Parse converts a raw string into a Version, supporting numeric dot-separated components,
// pre-release identifiers (after '-'), and build metadata (after '+').
// Examples: "1.2.3", "1.0.0-alpha", "1.0.0-beta.2", "1.0.0+build", "1.0.0-rc.1+build.123"
func Parse(raw string) (Version, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Version{}, fmt.Errorf("version string is empty")
	}

	// Split by '+' to separate build metadata
	var buildMeta string
	mainPart := trimmed
	if idx := strings.Index(trimmed, "+"); idx >= 0 {
		mainPart = trimmed[:idx]
		buildMeta = trimmed[idx+1:]
		if buildMeta == "" {
			return Version{}, fmt.Errorf("build metadata cannot be empty after '+'")
		}
		if !validIdentifierRegex.MatchString(buildMeta) {
			return Version{}, fmt.Errorf("invalid build metadata %q", buildMeta)
		}
	}

	// Split by '-' to separate pre-release identifier
	var preRelease string
	versionPart := mainPart
	if idx := strings.Index(mainPart, "-"); idx >= 0 {
		versionPart = mainPart[:idx]
		preRelease = mainPart[idx+1:]
		if preRelease == "" {
			return Version{}, fmt.Errorf("pre-release identifier cannot be empty after '-'")
		}
		if !validIdentifierRegex.MatchString(preRelease) {
			return Version{}, fmt.Errorf("invalid pre-release identifier %q", preRelease)
		}
	}

	// Parse the numeric version components
	parts := strings.Split(versionPart, ".")
	if len(parts) == 0 {
		return Version{}, fmt.Errorf("version string must have at least one numeric component")
	}

	components := make([]int, len(parts))
	for i, part := range parts {
		if part == "" {
			return Version{}, fmt.Errorf("invalid version component %q", part)
		}

		if !numericComponentRegex.MatchString(part) {
			return Version{}, fmt.Errorf("invalid version component %q", part)
		}

		value, err := strconv.Atoi(part)
		if err != nil {
			return Version{}, fmt.Errorf("invalid version component %q: %w", part, err)
		}
		components[i] = value
	}

	return Version{
		raw:        trimmed,
		components: components,
		preRelease: preRelease,
		buildMeta:  buildMeta,
	}, nil
}

// MustParse converts a raw string into a Version, panicking if parsing fails.
func MustParse(raw string) Version {
	v, err := Parse(raw)
	if err != nil {
		panic(err)
	}
	return v
}

// IsValid reports whether the provided string can be parsed into a Version.
func IsValid(raw string) bool {
	_, err := Parse(raw)
	return err == nil
}

// Compare returns 1 if the receiver is greater than other, -1 if less, and 0 if equal.
// Build metadata is ignored in comparisons. Pre-release versions have lower precedence
// than normal versions (e.g., 1.0.0-alpha < 1.0.0).
func (v Version) Compare(other Version) int {
	// First compare numeric components
	maxLen := max(len(v.components), len(other.components))

	for i := 0; i < maxLen; i++ {
		var a, b int
		if i < len(v.components) {
			a = v.components[i]
		}
		if i < len(other.components) {
			b = other.components[i]
		}

		if a > b {
			return 1
		}
		if a < b {
			return -1
		}
	}

	// If numeric components are equal, compare pre-release identifiers
	// No pre-release (normal version) > has pre-release (pre-release version)
	if v.preRelease == "" && other.preRelease != "" {
		return 1
	}
	if v.preRelease != "" && other.preRelease == "" {
		return -1
	}
	if v.preRelease != "" && other.preRelease != "" {
		if v.preRelease > other.preRelease {
			return 1
		}
		if v.preRelease < other.preRelease {
			return -1
		}
	}

	// Build metadata is ignored in comparisons
	return 0
}

// String returns the normalized string representation of the version.
func (v Version) String() string {
	if v.raw != "" {
		return v.raw
	}

	if len(v.components) == 0 {
		return ""
	}

	strParts := make([]string, len(v.components))
	for i, component := range v.components {
		strParts[i] = strconv.Itoa(component)
	}

	result := strings.Join(strParts, ".")
	if v.preRelease != "" {
		result += "-" + v.preRelease
	}
	if v.buildMeta != "" {
		result += "+" + v.buildMeta
	}

	return result
}

// Components returns a copy of the underlying version components.
func (v Version) Components() []int {
	if len(v.components) == 0 {
		return nil
	}
	result := make([]int, len(v.components))
	copy(result, v.components)
	return result
}

// PreRelease returns the pre-release identifier (the part after '-' and before '+').
// Returns empty string if there is no pre-release identifier.
func (v Version) PreRelease() string {
	return v.preRelease
}

// BuildMeta returns the build metadata (the part after '+').
// Returns empty string if there is no build metadata.
func (v Version) BuildMeta() string {
	return v.buildMeta
}

// CompareStrings parses the provided strings as versions and compares them.
func CompareStrings(a, b string) (int, error) {
	vA, err := Parse(a)
	if err != nil {
		return 0, fmt.Errorf("invalid version %q: %w", a, err)
	}

	vB, err := Parse(b)
	if err != nil {
		return 0, fmt.Errorf("invalid version %q: %w", b, err)
	}

	return vA.Compare(vB), nil
}
