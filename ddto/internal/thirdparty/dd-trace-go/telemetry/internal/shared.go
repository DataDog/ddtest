// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package internal

import (
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/globalconfig"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/internal/transport"
)

// SharedApp is a telemetry application that several processes report as
// one: each request carries its runtime ID, session headers, application
// and host instead of the sending process's.
type SharedApp struct {
	RuntimeID     string                `json:"runtime_id"`
	RootSessionID string                `json:"root_session_id"`
	Application   transport.Application `json:"application"`
	Host          transport.Host        `json:"host"`
}

// NewSharedApp describes this process's application and host under
// runtimeID, with this process's root session. The language version is left
// empty: each process that reports the application fills in its own.
func NewSharedApp(runtimeID string, config TracerConfig) SharedApp {
	body := newBody(config, false, nil)
	app := SharedApp{
		RuntimeID:     runtimeID,
		RootSessionID: globalconfig.RootSessionID(),
		Application:   body.Application,
		Host:          body.Host,
	}
	app.Application.LanguageVersion = ""
	return app
}
