// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package telemetry

import "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/internal"

// SharedApp is a telemetry application that several processes report as
// one. ddto's Git worker owns the application of an invocation and hands it
// to the test processes that it starts; see [ClientConfig.Shared].
type SharedApp = internal.SharedApp

// NewSharedApp describes this process's application and host under
// runtimeID, as [NewClient] would for service, env and version.
func NewSharedApp(runtimeID, service, env, version string) SharedApp {
	return internal.NewSharedApp(runtimeID, internal.TracerConfig{Service: service, Env: env, Version: version})
}

// sharedChild reports whether the client reports another process's
// application, whose owner sends app-started and app-closing.
func (c *client) sharedChild() bool {
	return c.clientConfig.Shared != nil && !c.clientConfig.SharedOwner
}
