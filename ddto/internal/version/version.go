// Package version identifies the CLI and native CI runtime independently of the upstream SDK base.
package version

// Number identifies this experimental CI runtime, independently of its source SDK.
// On main it is the last published release with a -dev suffix. The release
// workflow sets the release's version in the commit that the release tag
// points to, then opens a pull request that sets the new -dev value here.
const Number = "0.0.0-dev"

// Tag is the version reported by the extracted diagnostics and telemetry.
const Tag = "v" + Number

// BuildLogPrefix identifies diagnostics from the CLI and its build tools.
const BuildLogPrefix = "TestOptimization.build " + Tag

// RunLogPrefix identifies diagnostics from the native test runtime.
const RunLogPrefix = "TestOptimization.run  " + Tag

// SDKVersion and SDKCommit pin the extraction and differential reference together.
const SDKVersion = "v2.12.0-dev.3.0.20261008222249-870449702d0a"
const SDKCommit = "870449702d0a0cea26a6223eefe2f0a198069d79"
