// Package version identifies the native CI runtime independently of its upstream SDK base.
package version

// Number identifies this experimental CI runtime, independently of its source SDK.
const Number = "0.0.0"

// Tag is the version reported by the extracted diagnostics and telemetry.
const Tag = "v" + Number

// SDKVersion and SDKCommit pin the extraction and differential reference together.
const SDKVersion = "v2.12.0-dev.3.0.20261002145613-96aedb31048c"
const SDKCommit = "96aedb31048c07e29e7a20a4333dc3b8d289c52d"
