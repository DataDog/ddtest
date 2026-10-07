// Package version identifies the CLI and native CI runtime independently of the upstream SDK base.
package version

// Number identifies this experimental CI runtime, independently of its source SDK.
const Number = "0.0.0"

// Tag is the version reported by the extracted diagnostics and telemetry.
const Tag = "v" + Number

// BuildLogPrefix identifies diagnostics from the CLI and its build tools.
const BuildLogPrefix = "TestOptimization.build " + Tag

// RunLogPrefix identifies diagnostics from the native test runtime.
const RunLogPrefix = "TestOptimization.run  " + Tag

// SDKVersion and SDKCommit pin the extraction and differential reference together.
const SDKVersion = "v2.12.0-dev.3.0.20261002145613-96aedb31048c"
const SDKCommit = "96aedb31048c07e29e7a20a4333dc3b8d289c52d"
