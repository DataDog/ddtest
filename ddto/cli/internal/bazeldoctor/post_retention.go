package bazeldoctor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/ddto/cli/internal/pyformat"
)

// This file is the uploader's retention.py and _cleanup_staging: before
// releasing its staging, the run keeps every payload that it did not
// deliver, in a durable destination when one is configured.

type retentionOutcome struct {
	catalog       string
	sourceCount   int
	originalRoots []string
}

// cleanupStaging is _cleanup_staging.
func (p *poster) cleanupStaging(prep *preparation, report *aggregateReport, reason *reportReason, raw postDiscovery,
	retention *postDiscovery, run uploadRun, expected []string) (*aggregateReport, *reportReason) {
	cfg := p.cfg
	cleanupSafe := prep != nil
	preserve := false
	var details map[string]any
	recordFailure := func(code, message string) {
		cleanupSafe = false
		p.log.errorf("%s", message)
		if report == nil {
			report = p.emptyReport(2, raw)
		}
		if report.exitCode == 0 {
			report.exitCode = 2
		}
		report.warnings = uniqueCodes(append(report.warnings, code))
		if reason == nil || reason.code == "" || reason.code == "ok" {
			reason = &reportReason{code, message, []string{"Preserve the announced original outputs before cleaning temporary files."}}
		}
	}
	var stagedRoots []string
	if prep != nil {
		stagedRoots = prep.stagedRoots
	}
	if !cfg.DryRun {
		err := func() error {
			if retention == nil {
				var roots []scanRoot
				for _, root := range stagedRoots {
					roots = append(roots, scanRoot{root, true})
				}
				disc, err := p.discoverTasks(roots, nil)
				if err != nil {
					return err
				}
				retention = &disc
			}
			reasonCode := "unknown"
			if reason != nil {
				reasonCode = reason.code
			}
			outcome, err := p.retainPending(*retention, report, prep, run, expected, reasonCode)
			if err != nil {
				return err
			}
			for _, root := range stagedRoots {
				if hasIncompleteMaterialization(root) {
					preserve, cleanupSafe = true, false
					p.log.warnf("preserving incomplete BEP materialization in owned staging")
					break
				}
			}
			if outcome.sourceCount == 0 {
				return nil
			}
			details = map[string]any{"sources": outcome.sourceCount, "catalog": outcome.catalog,
				"durable_destination_configured": cfg.RetainedOutputsDir != "", "original_roots": reportList(outcome.originalRoots)}
			if outcome.catalog != "" {
				p.log.warnf("retained Test Optimization outputs: %s", outcome.catalog)
				return nil
			}
			p.log.warnf("unresolved Test Optimization outputs: roots=%d first=%s; configure a durable retention/artifact destination for ephemeral CI",
				len(outcome.originalRoots), outcome.originalRoots[0])
			for _, source := range outcome.originalRoots {
				for _, root := range stagedRoots {
					if source == root || isBelow(source, root) {
						preserve = true
					}
				}
				if cfg.WrapperTempRoot != "" && (source == resolvePath(cfg.WrapperTempRoot) || isBelow(source, resolvePath(cfg.WrapperTempRoot))) {
					cleanupSafe = false
				}
			}
			return nil
		}()
		if err != nil {
			preserve = true
			details = map[string]any{"status": "failed", "durable_destination_configured": cfg.RetainedOutputsDir != ""}
			recordFailure("output_retention_failed", "failed to retain outputs; preserve original roots: "+err.Error())
		}
	}
	switch {
	case prep != nil && !preserve:
		if err := p.cleanupPreparation(prep); err != nil {
			recordFailure("staging_cleanup_failed", "failed to clean BEP staging: "+err.Error())
		} else {
			p.log.debugf("BEP staging cleanup completed")
		}
	case preserve:
		cleanupSafe = false
		first := "unknown"
		if len(stagedRoots) > 0 {
			first = stagedRoots[0]
		}
		p.log.warnf("preserving this invocation's BEP staging roots: count=%d first=%s", len(stagedRoots), first)
	}
	if err := writeRetentionStatus(cfg.RetentionStatusFile, cfg.WrapperTempRoot, cleanupSafe); err != nil {
		recordFailure("retention_status_failed", "failed to publish temporary-output status: "+err.Error())
	}
	if report != nil && len(details) > 0 {
		report.retention = details
	}
	return report, reason
}

func hasIncompleteMaterialization(root string) bool {
	found := false
	_ = filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(entry.Name(), ".test.outputs.tmp-") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// copyVerified is _copy_verified: a regular file copied, hashed, and checked
// against its copy.
func copyVerified(source, destination string) (map[string]any, error) {
	before, err := os.Lstat(source)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errors.New("retention source must be a regular file")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return nil, err
	}
	after, err := os.Lstat(source)
	if err != nil || !os.SameFile(before, after) || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return nil, errors.New("retention source changed during copy")
	}
	writer, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(writer, bytes.NewReader(data)); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	copied, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(copied, data) {
		return nil, errors.New("retained file failed copy verification")
	}
	digest := sha256.Sum256(data)
	return map[string]any{"sha256": hex.EncodeToString(digest[:]), "bytes": len(data)}, nil
}

// retainPending is retain_pending_outputs.
func (p *poster) retainPending(disc postDiscovery, report *aggregateReport, prep *preparation, run uploadRun,
	expected []string, reasonCode string) (retentionOutcome, error) {
	cfg := p.cfg
	var pending []postTask
	for _, task := range disc.tasks {
		result, ok := run.byID[task.id]
		if cfg.KeepPayloads || !ok || result.Status == "failed" {
			pending = append(pending, task)
		}
	}
	if len(pending) == 0 {
		return retentionOutcome{}, nil
	}
	rootSet := map[string]bool{}
	for _, task := range pending {
		root := task.outputDir
		if root == "" {
			root = pathParent(task.sourcePath)
		}
		rootSet[resolvePath(root)] = true
	}
	roots := sortedKeys(rootSet)
	if cfg.RetainedOutputsDir == "" {
		for _, task := range pending {
			if info, err := os.Lstat(task.sourcePath); err != nil || !info.Mode().IsRegular() {
				return retentionOutcome{}, errors.New("unresolved source must remain a regular file")
			}
		}
		return retentionOutcome{sourceCount: len(pending), originalRoots: roots}, nil
	}
	destination := resolvePath(cfg.RetainedOutputsDir)
	forbidden := append([]string{cfg.ArtifactStagingDir}, roots...)
	if cfg.WrapperTempRoot != "" {
		forbidden = append(forbidden, cfg.WrapperTempRoot)
	}
	for _, root := range forbidden {
		resolved := resolvePath(root)
		if destination == resolved || isBelow(destination, resolved) {
			return retentionOutcome{}, errors.New("retention destination is inside a disposable or source root")
		}
	}
	if err := os.MkdirAll(destination, 0o777); err != nil {
		return retentionOutcome{}, err
	}
	owned, err := os.MkdirTemp(destination, "upload-")
	if err != nil {
		return retentionOutcome{}, err
	}
	owned = purePath(owned)
	status := func(id string) any {
		if result, ok := run.byID[id]; ok {
			return result.Status
		}
		return "unknown"
	}
	entries := []any{}
	for index, task := range pending {
		directory := joinPath(owned, fmt.Sprintf("source-%06d", index))
		if err := os.Mkdir(directory, 0o777); err != nil {
			return retentionOutcome{}, err
		}
		payload := joinPath(directory, "payload.json")
		copied, err := copyVerified(task.sourcePath, payload)
		if err != nil {
			return retentionOutcome{}, err
		}
		disposition := "unacknowledged"
		if cfg.KeepPayloads {
			disposition = "debug_original"
		}
		entry := map[string]any{"task_id": task.id, "target": nullable(task.label), "source_name": pathName(task.sourcePath),
			"output_key": nullable(task.outputKey), "payload_type": task.kind, "payload": relativePath(payload, owned),
			"sha256": copied["sha256"], "bytes": copied["bytes"], "disposition": disposition, "outcome": status(task.id)}
		if task.outputDir != "" {
			sidecar := joinPath(task.outputDir, "bazel_target_metadata.json")
			if exists(sidecar) || isSymlink(sidecar) {
				target := joinPath(directory, "bazel_target_metadata.json")
				copiedSidecar, err := copyVerified(sidecar, target)
				if err != nil {
					return retentionOutcome{}, err
				}
				entry["metadata"] = map[string]any{"path": relativePath(target, owned), "sha256": copiedSidecar["sha256"],
					"bytes": copiedSidecar["bytes"]}
			}
		}
		entries = append(entries, entry)
	}
	contexts, facts := []any{}, []any{}
	resources := p.request.Resources
	for index, record := range resources.ContextRecords {
		body := []byte(pyformat.DumpsIndented(map[string]any(resources.Contexts[record.Context])))
		path := joinPath(owned, fmt.Sprintf("context-%06d.json", index))
		if err := os.WriteFile(path, body, 0o666); err != nil {
			return retentionOutcome{}, err
		}
		digest := sha256.Sum256(body)
		contexts = append(contexts, map[string]any{"repo_key": record.RepoKey, "path": pathName(path),
			"sha256": hex.EncodeToString(digest[:])})
	}
	for _, task := range pending {
		if task.kind != "telemetry" {
			continue
		}
		for index, source := range resources.TelemetryFactsPaths {
			target := joinPath(owned, fmt.Sprintf("telemetry-facts-%06d.json", index))
			copied, err := copyVerified(realPath(source), target)
			if err != nil {
				return retentionOutcome{}, err
			}
			facts = append(facts, map[string]any{"path": pathName(target), "sha256": copied["sha256"], "bytes": copied["bytes"]})
		}
		break
	}
	executions, deficits := []any{}, map[string]any{}
	if prep != nil {
		plan := prep.plan
		for _, e := range plan.executions {
			executions = append(executions, map[string]any{"identity": []any{e.invocation, e.label, e.configuration, e.run,
				e.shard, e.attempt}, "cached": e.cached, "status": e.status, "output_keys": reportList(sortedStrings(e.outputKeys))})
		}
		proven, labels, empty := testEvidence(disc, run.byID)
		for _, label := range expected {
			labels[label] = true
		}
		for _, task := range disc.tasks {
			if task.label != "" {
				labels[task.label] = true
			}
		}
		shards := []any{}
		if shard, missing := missingTestShards(plan.executions, proven, labels, empty); missing {
			shards = append(shards, []any{shard.invocation, shard.label, shard.configuration, shard.run, shard.shard})
		}
		missingExpected := []string{}
		if plan.selectedSource == "bep" {
			represented := representedTargets(plan)
			for _, label := range expected {
				if !represented[label] {
					missingExpected = append(missingExpected, label)
				}
			}
		}
		deficits = map[string]any{"missing_test_shards": shards, "missing_expected_targets": reportList(sortedStrings(missingExpected)),
			"missing_output_labels": reportList(sortedKeys(plan.missingOutputLabels)),
			"ambiguous_output_keys": reportList(sortedKeys(plan.ambiguousOutputs)),
			"ambiguous_targets":     reportList(sortedKeys(plan.ambiguousLabels))}
	}
	outcomes := []any{}
	for _, task := range disc.tasks {
		entry := map[string]any{"task_id": task.id, "target": nullable(task.label), "output_key": nullable(task.outputKey),
			"outcome": status(task.id), "test_events": nil, "requests_attempted": nil, "requests_succeeded": nil,
			"request_ids": []any{}, "failure_code": nil}
		if result, ok := run.byID[task.id]; ok {
			entry["test_events"], entry["requests_attempted"] = result.TestEvents, result.Requests.Attempted
			entry["requests_succeeded"], entry["request_ids"] = result.Requests.Succeeded, reportList(result.RequestIDs)
			entry["failure_code"] = nullable(result.Failure)
		}
		outcomes = append(outcomes, entry)
	}
	var exitCode any
	if report != nil {
		exitCode = report.exitCode
	}
	catalog := map[string]any{"schema_version": 1, "purpose": "private_inspection", "sources": entries,
		"contexts": contexts, "executions": executions, "telemetry_facts": facts, "source_outcomes": outcomes,
		"unfulfilled_requirements": deficits, "expected_targets": reportList(sortedStrings(expected)),
		"uploader_reason_code": reasonCode, "uploader_exit_code": exitCode, "automatic_replay_supported": false}
	if report != nil && len(report.requests) > 0 {
		requests := []any{}
		for _, request := range report.requests {
			var succeeded any
			if request.Succeeded != nil {
				succeeded = *request.Succeeded
			}
			requests = append(requests, map[string]any{"request_id": request.ID, "source_ids": reportList(request.Sources),
				"attempts": request.Attempts, "succeeded": succeeded})
		}
		catalog["requests"] = requests
	}
	pendingPath := joinPath(owned, "catalog.pending")
	if err := os.WriteFile(pendingPath, []byte(pyformat.DumpsIndented(catalog)+"\n"), 0o666); err != nil {
		return retentionOutcome{}, err
	}
	if err := os.Rename(pendingPath, joinPath(owned, "catalog.json")); err != nil {
		return retentionOutcome{}, err
	}
	return retentionOutcome{joinPath(owned, "catalog.json"), len(pending), roots}, nil
}

func nullable(text string) any {
	if text == "" {
		return nil
	}
	return text
}

func sortedStrings(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		set[value] = true
	}
	return sortedKeys(set)
}

// writeRetentionStatus is write_retention_status.
func writeRetentionStatus(path, wrapperRoot string, cleanupSafe bool) error {
	if path == "" {
		return nil
	}
	if wrapperRoot == "" || !isBelow(resolvePath(path), resolvePath(wrapperRoot)) {
		return errors.New("retention status must be inside the wrapper's temporary root")
	}
	document := map[string]any{"schema_version": 1, "wrapper_temp_root": resolvePath(wrapperRoot), "cleanup_safe": cleanupSafe}
	temporary, err := os.CreateTemp(pathParent(path), ".retention-")
	if err != nil {
		return err
	}
	_, writeErr := temporary.WriteString(pyformat.DumpsIndented(document) + "\n")
	if err := errors.Join(writeErr, temporary.Close()); err != nil {
		_ = os.Remove(temporary.Name())
		return err
	}
	return os.Rename(temporary.Name(), path)
}
