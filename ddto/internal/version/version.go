// Package version identifies the CLI and native CI runtime independently of the upstream SDK base.
package version

// Number identifies this experimental CI runtime, independently of its source SDK.
// It is ddtest's version: ddtest's make release sets the release's version
// for the build, through scripts/release -version, without changing the
// checkout, whose 0.0.0-dev development builds report.
const Number = "0.0.0-dev"

// LibraryName names, beside the version, the library that reports Mini's
// events: ddtest, whose instrument command runs ddto. The SDK, which reports
// only its version, sends no name.
const LibraryName = "ddtest"

// Tag is the version reported by the extracted diagnostics and telemetry.
const Tag = "v" + Number

// BuildLogPrefix identifies diagnostics from the CLI and its build tools.
const BuildLogPrefix = "TestOptimization.build " + Tag

// RunLogPrefix identifies diagnostics from the native test runtime.
const RunLogPrefix = "TestOptimization.run  " + Tag

// SDKVersion and SDKCommit pin the extraction and differential reference together.
const SDKVersion = "v2.12.0-dev.3.0.20261008222249-870449702d0a"
const SDKCommit = "870449702d0a0cea26a6223eefe2f0a198069d79"
