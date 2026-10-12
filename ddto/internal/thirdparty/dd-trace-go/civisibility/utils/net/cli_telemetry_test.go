// Copyright 2026 Datadog, Inc. Licensed under the Apache License, Version 2.0.

package net

import (
	"os"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/telemetrytest"
)

// The worker takes app-closing's number before StopApp takes the final data,
// so that a fallback of the sequence is logged in that request.
func TestCLITelemetryStopReportsSequenceFallback(t *testing.T) {
	dir := t.TempDir()
	sequence, err := utils.WriteCLITelemetry(dir, telemetry.NewSharedApp("cli-runtime", "service", "env", ""))
	if err != nil {
		t.Fatal(err)
	}
	sequence.Next() // app-started
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	record := &telemetrytest.RecordClient{}
	t.Cleanup(telemetry.MockClient(record))
	app := &CLITelemetry{owner: cliTelemetryOwner{sequence: sequence}}
	app.Stop()
	for _, line := range record.Logs {
		if line.Text == "civisibility: CLI telemetry sequence unavailable" {
			return
		}
	}
	t.Fatalf("logs before app-closing: %+v", record.Logs)
}
