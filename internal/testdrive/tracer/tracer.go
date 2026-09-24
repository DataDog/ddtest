// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package tracer reuses project tracers or installs them for local testdrive runs.
package tracer

import "context"

// Installation identifies the tracer to use for a testdrive.
type Installation struct {
	Path    string
	Project bool
}

// Tracer reuses the project tracer, installing one only when absent.
type Tracer interface {
	Install(ctx context.Context, sessionDirectory string) (Installation, error)
}
