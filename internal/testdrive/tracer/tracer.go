// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package tracer installs language tracers for local testdrive runs.
package tracer

import "context"

// Tracer makes a Test Optimization tracer available inside a testdrive session.
type Tracer interface {
	Install(ctx context.Context, sessionDirectory string) (preloadPath string, err error)
}
