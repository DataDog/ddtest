// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package usagetelemetry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
)

// TroubleshootingError contains an error classification and the DDTest call
// stack at observation, never err.Error(), a recovered value or customer frames.
type TroubleshootingError struct {
	Operation string       `json:"operation"`
	Category  string       `json:"category"`
	Origin    string       `json:"stack_origin"`
	Frames    []OwnedFrame `json:"frames,omitempty"`
}

type OwnedFrame struct {
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}

// ObserveTroubleshootingError records only typed metadata. It neither retains
// the error nor sends anything. Ordinary writers do not capture a stack.
func ObserveTroubleshootingError(output io.Writer, operation string, err error) {
	if observer, ok := output.(interface{ ObserveTroubleshootingError(string, error) }); ok && err != nil {
		observer.ObserveTroubleshootingError(operation, err)
	}
}

func (a *Attempt) ObserveTroubleshootingError(operation string, err error) {
	if a == nil || a.finished || err == nil {
		return
	}
	// Prefer a typed execution error over the fallback result observation.
	detail := captureTroubleshootingError(operation, err)
	if a.troubleshootingError == nil || a.troubleshootingError.Category == "reported_failure" ||
		(a.troubleshootingError.Category == "unknown" && detail.Category != "unknown") {
		a.troubleshootingError = detail
	}
}

func captureTroubleshootingError(operation string, err error) *TroubleshootingError {
	detail := &TroubleshootingError{Operation: operation, Category: "reported_failure", Origin: "observation"}
	if err != nil {
		detail.Category = "unknown"
		var syntax *json.SyntaxError
		var mismatch *json.UnmarshalTypeError
		var file *fs.PathError
		var process *exec.ExitError
		var network net.Error
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			detail.Category = "timeout"
		case errors.Is(err, context.Canceled):
			detail.Category = "cancelled"
		case errors.As(err, &syntax), errors.As(err, &mismatch):
			detail.Category = "invalid_json"
		case errors.Is(err, io.ErrUnexpectedEOF):
			detail.Category = "unexpected_eof"
		case errors.As(err, &file):
			detail.Category = "filesystem"
		case errors.As(err, &process):
			detail.Category = "subprocess_exit"
		case errors.As(err, &network):
			detail.Category = "network"
		}
	}
	pcs := make([]uintptr, 32)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
	for {
		frame, more := frames.Next()
		if suffix, owned := strings.CutPrefix(frame.Function, "github.com/DataDog/ddtest/internal/"); owned && !strings.HasPrefix(suffix, "usagetelemetry.") {
			pkg, _, _ := strings.Cut(suffix, ".")
			detail.Frames = append(detail.Frames, OwnedFrame{Function: frame.Function, File: "internal/" + pkg + "/" + filepath.Base(frame.File), Line: frame.Line})
		}
		if !more || len(detail.Frames) == 8 {
			break
		}
	}
	return detail.sanitized()
}

var ownedFunctionPattern = regexp.MustCompile(`^github\.com/DataDog/ddtest/internal/[a-z0-9_/]+\.[A-Za-z0-9_().*]+$`)
var ownedFilePattern = regexp.MustCompile(`^internal/[a-z0-9_/]+/[a-z0-9_]+\.go$`)
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (e *TroubleshootingError) sanitized() *TroubleshootingError {
	if e == nil {
		return nil
	}
	result := &TroubleshootingError{
		Operation: diagnosticEnum(e.Operation, "command", "result", "test_execution", "report_read"),
		Category:  diagnosticEnum(e.Category, "reported_failure", "invalid_json", "unexpected_eof", "filesystem", "subprocess_exit", "network", "timeout", "cancelled", "unknown"),
		Origin:    diagnosticEnum(e.Origin, "observation"),
	}
	for _, frame := range e.Frames[:min(len(e.Frames), 8)] {
		pkg, _, _ := strings.Cut(strings.TrimPrefix(frame.Function, "github.com/DataDog/ddtest/"), ".")
		if len(frame.Function) <= 200 && len(frame.File) <= 150 && ownedFunctionPattern.MatchString(frame.Function) &&
			ownedFilePattern.MatchString(frame.File) && path.Dir(frame.File) == pkg && frame.Line > 0 && frame.Line <= 100_000 {
			result.Frames = append(result.Frames, frame)
		}
	}
	return result
}

func diagnosticBuildCommit() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Path == "github.com/DataDog/ddtest" {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && commitPattern.MatchString(setting.Value) {
				return setting.Value
			}
		}
	}
	return ""
}
