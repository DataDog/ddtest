// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

type retainedExecution struct {
	Status string            `json:"status"`
	Reason string            `json:"reason"`
	Result *validationResult `json:"result"`
}

func hasPairedExecution(result validationResult) bool {
	if result.CheckOnly || result.Framework != "jest" {
		return false
	}
	baseline, instrumented := false, false
	for _, run := range result.Runs {
		if run.Command == "" || run.ExitCode == nil {
			continue
		}
		baseline = baseline || (run.Name == "baseline" && !run.Instrumented)
		instrumented = instrumented || (run.Name == "reporting-only" && run.Instrumented)
	}
	return baseline && instrumented
}

// Keep one historical paired run, never a growing chain of reports. Its verdict
// is not reused: config, source, dependencies or environment may have changed.
// As with probe execution, callers must run testdrive invocations sequentially.
func retainExecution(root string, result *validationResult) error {
	result.Retained = nil
	if hasPairedExecution(*result) {
		return nil
	}
	file, err := os.Open(validationPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("preserve earlier execution report: %w", err)
	}
	defer func() { _ = file.Close() }()
	// Reports contain summaries, not raw events. Do not read an arbitrary-size
	// existing file into memory, or overwrite evidence we could not read.
	const limit = 8 << 20
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return fmt.Errorf("preserve earlier execution report: %w", err)
	}
	if len(data) > limit {
		return fmt.Errorf("preserve earlier execution report: existing report exceeds %d bytes; left unchanged", limit)
	}
	var previous validationResult
	if err := json.Unmarshal(data, &previous); err != nil {
		return fmt.Errorf("preserve earlier execution report: existing report is unreadable; left unchanged: %w", err)
	}
	if !hasPairedExecution(previous) {
		if previous.Retained == nil || previous.Retained.Result == nil {
			return nil
		}
		previous = *previous.Retained.Result
	}
	if !hasPairedExecution(previous) {
		return nil
	}
	previous.Retained = nil
	result.Retained = &retainedExecution{
		Status: "historical",
		Reason: "Earlier execution evidence only; not revalidated by this invocation. Rerun full validation after changes to the test command, configuration, source, dependencies, runtime, or tracer. Earlier failures remain unresolved until rechecked.",
		Result: &previous,
	}
	return nil
}
