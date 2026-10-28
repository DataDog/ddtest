// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package onboard

// ciProviders registers every CI provider onboarding reads, in selection
// order; the first is the default for a repository without CI configuration.
// Everything else goes through ciProvider (see ci_provider.go).
var ciProviders = []ciProvider{gitHubActions{}}
