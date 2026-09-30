package framework

import "context"

// Platform supplies the runtime services shared by all test frameworks.
// Implementations live in package platform; this consumer interface avoids an import cycle.
type Platform interface {
	Name() string
	SanityCheck(context.Context) error
	// RunEnv prepares execution; the command layer calls SanityCheck before running.
	RunEnv(RuntimeOptions) (map[string]string, error)
	// DiscoveryEnv validates any tracer prerequisite before preparing discovery.
	DiscoveryEnv(context.Context, DiscoveryKind, RuntimeOptions) (map[string]string, error)
}

// RuntimeOptions describes the framework's environment and additional startup files.
// Env overrides inherited values, including explicitly empty values. Methods return
// a fresh map and never modify Env. PreloadFiles are appended after project loaders.
type RuntimeOptions struct {
	Framework    string
	Env          map[string]string
	PreloadFiles []string
}

// DiscoveryKind distinguishes native file discovery from tracer-based full discovery.
type DiscoveryKind uint8

const (
	FileDiscovery DiscoveryKind = iota
	FullDiscovery
)
