package version

import (
	"fmt"
	"strconv"
	"strings"
)

// Version represents a semantic-like version composed of dot-separated integer components.
type Version struct {
	raw        string
	components []int
}

// Parse converts a raw string into a Version, ensuring it only contains numeric dot-separated components.
func Parse(raw string) (Version, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Version{}, fmt.Errorf("version string is empty")
	}

	parts := strings.Split(trimmed, ".")
	components := make([]int, len(parts))

	for i, part := range parts {
		if part == "" {
			return Version{}, fmt.Errorf("invalid version component %q", part)
		}

		for _, r := range part {
			if r < '0' || r > '9' {
				return Version{}, fmt.Errorf("invalid version component %q", part)
			}
		}

		value, err := strconv.Atoi(part)
		if err != nil {
			return Version{}, fmt.Errorf("invalid version component %q: %w", part, err)
		}
		components[i] = value
	}

	return Version{
		raw:        strings.Join(parts, "."),
		components: components,
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
func (v Version) Compare(other Version) int {
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

	return strings.Join(strParts, ".")
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
