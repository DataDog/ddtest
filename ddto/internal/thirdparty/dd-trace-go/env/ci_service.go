// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package env

// Mini-only configuration extends the upstream registry without hand-editing
// its generated file. The names also remain valid for configuration telemetry.
func init() {
	SupportedConfigurations["DD_CIVISIBILITY_SERVICE_FROM_CODEOWNERS"] = struct{}{}
	SupportedConfigurations["DD_CIVISIBILITY_SERVICE_FROM_CODEOWNERS_FORMAT"] = struct{}{}
}
