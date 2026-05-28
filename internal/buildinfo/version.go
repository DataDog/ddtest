package buildinfo

import "runtime/debug"

const defaultVersion = "dev"

// Version is set by release builds with -ldflags.
var Version = defaultVersion

func CurrentVersion() string {
	if Version != "" && Version != defaultVersion {
		return Version
	}

	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
	}

	if Version != "" {
		return Version
	}

	return defaultVersion
}
