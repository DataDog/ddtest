// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package testdrive

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/onboard"
)

// WorkflowEvidence stays in the existing bounded JSON; full test output stays
// in the upstream HTML. No new report or raw-event archive is created.
type WorkflowEvidence struct {
	Plan        *onboard.ExecutionPlan  `json:"plan,omitempty"`
	Fingerprint string                  `json:"input_fingerprint"`
	Node        string                  `json:"node"`
	FactsSHA256 string                  `json:"facts_sha256,omitempty"`
	Changes     []onboard.SetupChange   `json:"changes,omitempty"`
	Checks      []onboard.WorkflowCheck `json:"checks"`
}

func (t *Testdrive) verifyReportExclusions(ctx context.Context) []onboard.WorkflowCheck {
	reports := []string{".testoptimization/testdrive.json", ".testoptimization/report.html"}
	git := onboard.WorkflowCheck{Name: "git_report_exclusions", Status: "passed", Command: append([]string{"git", "check-ignore", "--no-index", "--"}, reports...)}
	data, err := t.executor.CombinedOutput(ctx, "git", git.Command[1:], map[string]string{"NODE_OPTIONS": ""})
	ignored := strings.Fields(string(data))
	if err != nil || !slices.Contains(ignored, reports[0]) || !slices.Contains(ignored, reports[1]) {
		git.Status = "failed"
		git.Reason = "Both retained reports must be ignored; add /.testoptimization/ to the repository .gitignore."
	}
	tracked, trackErr := t.executor.CombinedOutput(ctx, "git", append([]string{"ls-files", "--"}, reports...), nil)
	if trackErr != nil || len(strings.TrimSpace(string(tracked))) > 0 {
		git.Status = "failed"
		git.Reason = "Report tracking could not be ruled out. Review git ls-files -- .testoptimization; existing tracked reports require an explicit untracking decision."
	}
	pack := onboard.WorkflowCheck{Name: "package_report_exclusions", Status: "passed"}
	// Reports live at the root. A private root is not a publish target, and
	// workspace packages cannot package files from their parent directory.
	var manifest struct {
		Private bool `json:"private"`
	}
	manifestData, manifestErr := os.ReadFile(filepath.Join(t.repositoryRoot, "package.json"))
	if manifestErr == nil {
		manifestErr = json.Unmarshal(manifestData, &manifest)
	}
	if manifestErr != nil {
		pack.Status, pack.Reason = "failed", "Cannot determine package publishability: "+reportText(manifestErr.Error())
		return []onboard.WorkflowCheck{git, pack}
	}
	if manifest.Private {
		pack.Status, pack.Reason = "not applicable", "Root package is private; root reports have no publish target. Git exclusions are checked separately."
		return []onboard.WorkflowCheck{git, pack, {Name: "package_check_cleanup", Status: "not applicable", Reason: "No package-check storage or process was created."}}
	}
	directory, err := os.MkdirTemp("", "ddtest-package-check-")
	if err != nil {
		pack.Status = "failed"
		pack.Reason = reportText(err.Error())
		return []onboard.WorkflowCheck{git, pack}
	}
	pack.Command = []string{"npm", "pack", "--dry-run", "--ignore-scripts", "--json", "--cache=" + filepath.Join(directory, "npm")}
	packCtx, cancel := context.WithTimeout(ctx, time.Minute)
	var stderr []byte
	if executor, ok := t.executor.(separateOutputExecutor); ok {
		data, stderr, err = executor.Output(packCtx, "npm", pack.Command[1:], map[string]string{"NODE_OPTIONS": ""})
	} else {
		err = fmt.Errorf("command executor cannot capture stdout and stderr separately")
	}
	cancel()
	if err != nil {
		pack.Status = "failed"
		pack.Reason = "Package file-list dry run failed; no publish or lifecycle script was requested: " + reportText(err.Error()+": "+commandDiagnostic(append(data, stderr...)))
	} else {
		var packages []struct {
			Files []struct {
				Path string `json:"path"`
			} `json:"files"`
		}
		if err := json.Unmarshal(data, &packages); err != nil || len(packages) == 0 {
			pack.Status = "failed"
			pack.Reason = "npm did not return a valid package file list."
		} else {
			count := 0
			for _, p := range packages {
				for _, file := range p.Files {
					count++
					path := strings.TrimPrefix(filepath.ToSlash(file.Path), "./")
					if path == ".testoptimization" || strings.HasPrefix(path, ".testoptimization/") {
						pack.Status = "failed"
						pack.Reason = "npm would include local validation reports; correct the package files/.npmignore configuration."
					}
				}
			}
			if pack.Status == "passed" {
				pack.Reason = fmt.Sprintf("Checked %d package file entries; reports excluded. Lifecycle scripts, archive creation and publishing disabled.", count)
			}
		}
	}
	cleanup := onboard.WorkflowCheck{Name: "package_check_cleanup", Status: "passed", Reason: "Owned package-check storage removed; shared caches were not scanned or pruned."}
	if err := os.RemoveAll(directory); err != nil {
		cleanup.Status = "failed"
		cleanup.Reason = reportText(err.Error())
	}
	return []onboard.WorkflowCheck{git, pack, cleanup}
}

func workflowChecksPassed(workflow *WorkflowEvidence) bool {
	if workflow == nil {
		return true
	}
	for _, check := range workflow.Checks {
		if check.Status != "passed" && check.Status != "not applicable" {
			return false
		}
	}
	return true
}
