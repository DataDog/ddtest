// Package bundle holds what a release build of ddto carries, so that it needs
// neither the module's sources nor a compiler for its own tools: a zip of the
// Mini runtime's sources and the tool wrapper executable for the platform that
// ddto was built for. scripts/release builds both and supplies them through
// -overlay with the ddto_release build tag. Other builds carry neither, and
// ddto uses the module's sources and builds the wrapper, as before.
package bundle

// RuntimeArchive is the name of the runtime zip in this package's directory.
const RuntimeArchive = "runtime.zip"

// ToolWrapperFile is the name of the wrapper executable in this package's
// directory.
const ToolWrapperFile = "toolwrap"

// Runtime returns the zip of the Mini runtime module's sources, or nil.
func Runtime() []byte { return runtime }

// ToolWrapper returns the tool wrapper executable, or nil.
func ToolWrapper() []byte { return toolWrapper }
