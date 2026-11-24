package bazeldoctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"time"
)

// This file is the uploader's application.py: one invocation, from the
// freshness preparation to the report. The Python uploader holds the
// workspace lock around it and resolves its configuration and resources.

// errPostInterrupted is the run's error when its context is cancelled.
var errPostInterrupted = errors.New("interrupted")

// postFallback asks the Python uploader to run instead: an input needs its
// own reading, and nothing was sent or printed.
type postFallback struct{ reason string }

func (f *postFallback) Error() string { return "fallback: " + f.reason }

// reportReason is _ReportReason.
type reportReason struct {
	code, message string
	nextSteps     []string
}

type poster struct {
	ctx       context.Context
	request   PostRequest
	cfg       PostConfig
	d         *doctor
	log       *postLog
	workers   int
	phases    []phaseTime
	committed bool
}

// preparation is FreshnessPreparation.
type preparation struct {
	plan        freshnessPlan
	scanRoots   []scanRoot
	stagedRoots []string
	staged      []stagedArtifact
	stagingBase string
}

// PostOutcome is how a post-process run ended: its exit status, or a
// fallback reason when the Python uploader must run instead.
type PostOutcome struct {
	ExitCode int
	Fallback string
}

// RunPost runs the uploader's invocation after its lock, configuration and
// resources: freshness, BEP artifact staging, discovery, the doctor's output
// checks, the upload with one decode of each payload, retention and the
// report.
func RunPost(ctx context.Context, request PostRequest, stdout, stderr io.Writer, workers int) PostOutcome {
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	cfg := request.Config
	level, _ := logLevel(os.Getenv)
	args := Args{FreshnessSource: cfg.FreshnessSource, FreshnessMode: cfg.FreshnessMode,
		ArtifactSource: cfg.ArtifactSource, RemoteArtifacts: cfg.RemoteArtifacts,
		ArtifactStagingDir: cfg.ArtifactStagingDir, BepArtifactDownloader: cfg.BepArtifactDownloader,
		BepArtifactDownloaderTimeout: cfg.BepArtifactDownloaderTimeout, BepJSON: cfg.BepJSONFiles}
	d := &doctor{ctx: ctx, request: Request{Args: args, ExecrootRoots: cfg.ExecrootRoots,
		OutputBaseRoots: cfg.OutputBaseRoots}, out: &output{level: level}, cache: newOutputs(),
		report: newReport(args), workers: workers, workspace: cfg.Workspace}
	p := &poster{ctx: ctx, request: request, cfg: cfg, d: d, workers: workers,
		log: newPostLog(cfg.LogLevel, stdout, stderr, os.Getenv("DD_API_KEY"))}
	if runtime.GOOS == "windows" {
		return PostOutcome{Fallback: "Windows paths"}
	}
	exitCode, err := p.run()
	var fallback *postFallback
	if errors.As(err, &fallback) {
		return PostOutcome{Fallback: fallback.reason}
	}
	return PostOutcome{ExitCode: exitCode}
}

// phase runs one invocation phase and records its wall and CPU time.
func (p *poster) phase(name string, run func() error) error {
	start, cpu := time.Now(), processCPU()
	err := run()
	used := processCPU()
	p.phases = append(p.phases, phaseTime{name, time.Since(start).Seconds(), used[0] + used[1] - cpu[0] - cpu[1]})
	return err
}

// doctorCall runs the doctor's code, relays what it printed, and turns its
// failure into a controlled error.
func (p *poster) doctorCall(run func()) (err error) {
	defer func() {
		p.log.relay(p.d.out)
		switch typed := recover().(type) {
		case nil:
		case failure:
			err = &postError{typed.message, 2}
		case fallback:
			err = &postFallback{typed.reason}
		case interrupted:
			err = errPostInterrupted
		default:
			panic(typed)
		}
	}()
	run()
	return nil
}

// commit prints what the run held: from now on it cannot fall back.
func (p *poster) commit() {
	p.committed = true
	p.log.commit()
}

func (p *poster) run() (int, error) {
	started := time.Now()
	startCPU := processCPU()
	plan := emptyPlan()
	var prep *preparation
	var rawDisc, eligibleDisc postDiscovery
	var retentionDisc *postDiscovery
	var report *aggregateReport
	var reason *reportReason
	var run uploadRun
	workersCompleted := false
	skippedCount := 0
	expected := p.cfg.ExpectedTargets
	doctorExit := 0
	p.logInvocation()

	controlled := func() error {
		if p.request.PreflightError != "" {
			return failRun(2, "%s", p.request.PreflightError)
		}
		localRoot, err := p.localTestlogsRoot()
		if err != nil {
			return err
		}
		if err := p.phase("freshness", func() error {
			var err error
			prep, err = p.prepareFreshness(localRoot, expected)
			return err
		}); err != nil {
			return err
		}
		plan = prep.plan
		p.log.debugf("freshness prepared: selected_source=%s scan_roots=%d staged_roots=%d eligible=%d cached=%d remote_only=%d skipped_targets=%d",
			plan.selectedSource, len(prep.scanRoots), len(prep.stagedRoots), len(plan.eligible), len(plan.cached),
			len(plan.remoteOnly), len(plan.skippedTargets))
		noExpected := noExpectedPayloads(expected, plan)
		if len(prep.scanRoots) == 0 && p.cfg.FailOnError && !noExpected {
			for _, label := range expected {
				if plan.ambiguousLabels[label] {
					return failRun(2, "BEP freshness is ambiguous: target reported as both platform-skipped and executed")
				}
			}
			return failRun(2, "FAIL_ON_ERROR is set and no local or staged testlogs root was found")
		}
		var selection filterResult
		if err := p.phase("discovery_matching", func() error {
			var err error
			if rawDisc, err = p.discoverPayloads(prep, noExpected); err != nil {
				return err
			}
			expectedDisc, err := selectExpectedOutputs(rawDisc, expected, plan.selectedSource == "bep", plan.eligible)
			if err != nil {
				return err
			}
			selection, err = filterForFreshness(expectedDisc, plan, p.cfg.FreshnessMode, expected)
			return err
		}); err != nil {
			return err
		}
		eligibleDisc = selection.discovery
		retained := p.retentionTasks(rawDisc, eligibleDisc, prep)
		retentionDisc = &retained
		skippedCount = len(selection.skipped)
		for _, key := range selection.skipped {
			p.log.debugf("freshness skipped output_key=%s", key)
		}
		p.logFreshnessMarkers(plan, selection.skipped)
		if len(eligibleDisc.tasks) > 0 && p.cfg.Agentless && !p.cfg.DryRun && os.Getenv("DD_API_KEY") == "" {
			return failRun(2, "DD_API_KEY required for agentless uploads")
		}
		if len(eligibleDisc.tasks) == 0 {
			p.log.debugf("credential validation skipped: no upload tasks")
		}
		// Sending deletes payloads: the run can no longer fall back.
		p.commit()
		if p.request.Validation != nil {
			p.prelistOutputs(eligibleDisc)
		}
		for _, message := range p.request.Resources.Messages {
			p.log.logf(strings.ToUpper(message.Level), "%s", message.Message)
		}
		var uploadErr error
		_ = p.phase("native_upload", func() error {
			run, uploadErr = p.upload(eligibleDisc)
			return uploadErr
		})
		if uploadErr != nil {
			return uploadErr
		}
		workersCompleted = true
		warnings := uniqueCodes(append(append(append([]string{}, eligibleDisc.warnings...), p.request.Resources.WarningCodes...),
			run.warnings...))
		for _, code := range warnings {
			p.log.warnf("pre-worker warning_code=%s", code)
		}
		if run.exitCode == 130 {
			warnings = append(warnings, "invocation_interrupted")
			p.log.errorf("interrupted after completed=%d cancelled=%d", len(run.results), run.cancelled)
		}
		p.logResults(run.results)
		report = &aggregateReport{dryRun: p.cfg.DryRun, exitCode: run.exitCode, configuredWorkers: max(1, p.cfg.Workers),
			discovered: rawDisc.counts(), results: run.results, cancelled: run.cancelled, warnings: warnings,
			requests: run.requests, nativeWorkers: run.workers}
		if run.exitCode == 130 {
			reason = &reportReason{"interrupted", "Uploader interrupted after active workers finished.",
				[]string{"Re-run the uploader to process retained and cancelled files."}}
		} else {
			warning, err := validateAccounted(plan, eligibleDisc, run.byID, expected, p.cfg.FailOnError,
				selection.identified, selection.ambiguous)
			var failed *postError
			switch {
			case errors.As(err, &failed):
				p.log.errorf("%s", failed.message)
				report.exitCode = failed.exitCode
				if strings.Contains(strings.ToLower(failed.message), "remote-only") {
					preflight := p.preflightFailureReason(plan, failed.message)
					reason = &preflight
				} else {
					reason = &reportReason{"fresh_output_without_payloads", failed.message,
						[]string{"Inspect the fresh test.outputs payload directories."}}
				}
			case warning != "":
				p.log.warnf("%s", warning)
				report.warnings = append(report.warnings, "bep_missing_test_evidence")
			}
		}
		if p.request.Validation != nil {
			doctorExit = p.validateOutputs(eligibleDisc, run)
		}
		if len(rawDisc.tasks) == 0 {
			if noExpected {
				reason = &reportReason{code: "ok", message: "All expected targets were cached or skipped as platform-incompatible; nothing was uploaded."}
			} else if testsExecuted(prep.scanRoots) && p.cfg.FailOnError {
				report.exitCode = 1
				reason = &reportReason{"tests_ran_without_payloads", "Tests ran but no payload files were found.",
					[]string{"Check that DD_TEST_OPTIMIZATION_PAYLOADS_IN_FILES=true is set."}}
			}
		}
		return nil
	}

	err := controlled()
	var failed *postError
	var fallback *postFallback
	switch {
	case err == nil:
	case errors.As(err, &fallback) && !p.committed:
		// The Python uploader stages again; its run directory is its own.
		_ = p.cleanupPreparation(prep)
		return 0, err
	case errors.As(err, &fallback):
		// Only the doctor's checks run after commit; they warn instead.
		p.log.warnf("doctor checks skipped: %s", fallback.reason)
	case errors.Is(err, errPostInterrupted):
		p.commit()
		report = p.emptyReport(130, rawDisc)
		reason = &reportReason{code: "interrupted", message: "Uploader interrupted during preparation."}
	case errors.As(err, &failed):
		p.commit()
		p.log.errorf("%s", failed.message)
		report = p.emptyReport(failed.exitCode, rawDisc)
		preflight := p.preflightFailureReason(plan, failed.message)
		reason = &preflight
	default:
		p.commit()
		p.log.errorf("%s", err)
		report = p.emptyReport(2, rawDisc)
		preflight := p.preflightFailureReason(plan, err.Error())
		reason = &preflight
	}
	_ = p.phase("staging_cleanup", func() error {
		report, reason = p.cleanupStaging(prep, report, reason, rawDisc, retentionDisc, run, expected)
		return nil
	})
	finished := processCPU()
	report.elapsed = time.Since(started).Seconds()
	report.phases = p.phases
	report.processCPU = &[2]float64{max(0, finished[0]-startCPU[0]), max(0, finished[1]-startCPU[1])}
	if doctorExit != 0 && report.exitCode == 0 {
		// The doctor's failure comes first; the payloads were still sent.
		report.exitCode = doctorExit
		if reason == nil || reason.code == "" || reason.code == "ok" {
			code, message, steps := p.d.report.classify(p.d.out.last)
			reason = &reportReason{code, message, steps}
		}
	}
	context := p.legacyContext(plan, rawDisc, prep, skippedCount, reason)
	p.log.debugf("emitting final report: exit_code=%d results=%d warnings=%v", report.exitCode, len(report.results), report.warnings)
	if workersCompleted {
		p.logResultMarkers(report.results)
	}
	p.emitReport(*report, context)
	return report.exitCode, nil
}

func (p *poster) emptyReport(exitCode int, disc postDiscovery) *aggregateReport {
	return &aggregateReport{dryRun: p.cfg.DryRun, exitCode: exitCode, configuredWorkers: max(1, p.cfg.Workers),
		discovered: disc.counts(), warnings: disc.warnings}
}

// localTestlogsRoot is resolve_local_testlogs_root.
func (p *poster) localTestlogsRoot() (string, error) {
	if p.cfg.TestlogsDir != "" {
		if !isDir(p.cfg.TestlogsDir) {
			return "", failRun(2, "TESTLOGS_DIR is not a directory: %s", p.cfg.TestlogsDir)
		}
		return resolvePath(p.cfg.TestlogsDir), nil
	}
	cwd, _ := os.Getwd()
	for _, candidate := range []string{joinPath(p.cfg.Workspace, "bazel-testlogs"), joinPath(cwd, "bazel-testlogs")} {
		if isDir(candidate) {
			return resolvePath(candidate), nil
		}
	}
	return "", nil
}

// prepareFreshness is prepare_freshness.
func (p *poster) prepareFreshness(localRoot string, expected []string) (*preparation, error) {
	cfg := p.cfg
	expectedSet := map[string]bool{}
	for _, label := range expected {
		expectedSet[label] = true
	}
	var warnings []string
	bepConfigured := len(cfg.BepJSONFiles) > 0
	stagingRequested := cfg.ArtifactSource == "bep" || cfg.ArtifactSource == "auto" && cfg.RemoteArtifacts != "disabled"
	if cfg.ArtifactSource == "bep" && !bepConfigured {
		return nil, failRun(2, "--artifact-source=bep requires --bep-json or DD_TEST_OPTIMIZATION_BEP_JSON")
	}
	source, err := p.selectSource(bepConfigured, &warnings)
	if err != nil {
		return nil, err
	}
	processing := source == "bep" || stagingRequested
	var bepFiles []string
	if processing {
		if !bepConfigured {
			return nil, failRun(2, "BEP artifact staging requires a configured BEP JSON file")
		}
		for _, path := range cfg.BepJSONFiles {
			resolved, err := p.resolveRuntimeFile(path)
			if err != nil {
				if cfg.FreshnessMode != "optional" || stagingRequested {
					return nil, err
				}
				warnings = append(warnings, "bep_freshness_unavailable")
				source, processing, bepFiles = "none", false, nil
				break
			}
			bepFiles = append(bepFiles, resolved)
		}
	}
	prep := &preparation{}
	var f *freshness
	selectedArtifacts, blocked, staged := map[pair]bool{}, map[string]bool{}, map[pair]bool{}
	if processing {
		var selectedLabels map[string]bool
		if len(expectedSet) > 0 {
			selectedLabels = expectedSet
		}
		p.d.out.asRuntime = true
		err := p.doctorCall(func() {
			defer func() { p.d.out.asRuntime = false }()
			f = p.d.parseFreshness(bepFiles, cfg.FreshnessMode != "optional" || stagingRequested, stagingRequested)
			if f == nil || !stagingRequested {
				return
			}
			selectedArtifacts = selectedArtifactOutputs(f, cfg.RemoteArtifacts)
			blocked = blockedArtifactLabels(f, cfg.RemoteArtifacts)
			prep.stagingBase = resolvePath(cfg.ArtifactStagingDir)
			prep.staged = p.d.stageArtifacts(f, prep.stagingBase, selectedLabels)
			applyStaged(f, prep.staged)
		})
		if p.d.out.warnings > 0 {
			warnings = append(warnings, "bep_runtime_warning")
			p.d.out.warnings = 0
		}
		if err != nil {
			return nil, err
		}
		for _, item := range prep.staged {
			staged[pair{item.label, item.outputKey}] = true
		}
	}
	plan, err := p.buildPlan(source, f, selectedArtifacts, blocked, expectedSet, warnings, staged)
	if err != nil {
		// Finalization retains and cleans what was staged, as the uploader's
		// staging owner does.
		prep.stagedRoots = stagedRootsOf(prep.staged)
		return prep, err
	}
	prep.plan = plan
	if localRoot != "" {
		prep.scanRoots = append(prep.scanRoots, scanRoot{localRoot, false})
	}
	prep.stagedRoots = stagedRootsOf(prep.staged)
	for _, root := range prep.stagedRoots {
		prep.scanRoots = append(prep.scanRoots, scanRoot{root, true})
	}
	for _, item := range prep.staged {
		p.log.debugf("BEP artifact staging selected output key: %s", item.outputKey)
	}
	p.log.debugf("freshness ready: source=%s eligible=%d cached=%d remote_only=%d skipped_targets=%d staged=%d",
		plan.selectedSource, len(plan.eligible), len(plan.cached), len(plan.remoteOnly), len(plan.skippedTargets),
		len(plan.stagedOutputs))
	for _, warning := range plan.warnings {
		p.log.warnf("preflight warning_code=%s", warning)
	}
	return prep, nil
}

func stagedRootsOf(staged []stagedArtifact) []string {
	roots := map[string]bool{}
	for _, item := range staged {
		roots[resolvePath(item.stagingRoot)] = true
	}
	return sortedKeys(roots)
}

// cleanupPreparation is FreshnessPreparation.cleanup.
func (p *poster) cleanupPreparation(prep *preparation) error {
	if prep == nil || len(prep.staged) == 0 {
		return nil
	}
	return p.doctorCall(func() { p.d.cleanupStagedRuns(prep.staged, prep.stagingBase) })
}

// discoverPayloads is _discover_payloads.
func (p *poster) discoverPayloads(prep *preparation, noExpected bool) (postDiscovery, error) {
	keys := map[string]bool{}
	for pr := range prep.plan.selectedArtifactOutputs {
		keys[pr.key] = true
	}
	selected := sortedKeys(keys)
	for _, key := range selected {
		p.log.debugf("freshness eligible output_key=%s", key)
	}
	scan := func() (postDiscovery, error) { return p.discoverTasks(prep.scanRoots, selected) }
	if noExpected {
		p.log.debugf("all expected targets were cached or platform-skipped; skipping discovery wait")
		return postDiscovery{}, nil
	}
	if len(prep.scanRoots) == 0 {
		p.log.debugf("no local or staged testlogs roots were found")
		return scan()
	}
	disc, reason, elapsed, err := p.waitForQuiescence(scan)
	if err != nil {
		return postDiscovery{}, err
	}
	p.log.debugf("discovery settled: reason=%s elapsed=%.2fs outputs=%d files=%d", reason, elapsed.Seconds(),
		len(disc.outputs), len(disc.tasks))
	return disc, nil
}

// retentionTasks is the retention discovery: the eligible tasks, and the
// raw tasks below the run's own staging roots.
func (p *poster) retentionTasks(raw, eligible postDiscovery, prep *preparation) postDiscovery {
	byID := map[string]postTask{}
	for _, task := range eligible.tasks {
		byID[task.id] = task
	}
	retained := postDiscovery{outputs: raw.outputs, warnings: raw.warnings}
	for _, task := range raw.tasks {
		if labeled, ok := byID[task.id]; ok {
			retained.tasks = append(retained.tasks, labeled)
			continue
		}
		resolved := resolvePath(task.sourcePath)
		for _, root := range prep.stagedRoots {
			if isBelow(resolved, root) {
				retained.tasks = append(retained.tasks, task)
				break
			}
		}
	}
	return retained
}

// preflightFailureReason is _preflight_failure_reason.
func (p *poster) preflightFailureReason(plan freshnessPlan, message string) reportReason {
	cfg := p.cfg
	lower := strings.ToLower(message)
	switch {
	case len(cfg.BepJSONFiles) == 0 && (cfg.FreshnessSource == "bep" || cfg.ArtifactSource == "bep" || cfg.FreshnessMode == "required"):
		return reportReason{"missing_bep_json", "BEP freshness or artifact staging was required, but no BEP JSON was configured.",
			[]string{"Pass --bep-json from the matching bazel test invocation."}}
	case len(plan.remoteOnly) > 0 || strings.Contains(lower, "remote-only"):
		return reportReason{"bep_output_remote_only_without_downloader",
			"BEP selected remote-only outputs that could not be materialized locally.",
			[]string{"Enable --remote-artifacts=download with a downloader, or adjust Bazel remote download settings."}}
	case len(plan.cached) > 0 && len(plan.eligible) == 0:
		return reportReason{"target_cached_by_bazel", "Cached Bazel outputs did not satisfy the requested freshness contract.",
			[]string{"Use the BEP from the exact matching bazel test invocation."}}
	}
	return reportReason{"upload_failed_unknown", message,
		[]string{"Correct the uploader configuration or freshness inputs and retry."}}
}

// legacyContext is _legacy_context.
func (p *poster) legacyContext(plan freshnessPlan, raw postDiscovery, prep *preparation, skipped int,
	reason *reportReason) legacyContext {
	cfg := p.cfg
	final := reportReason{}
	if reason != nil {
		final = *reason
	}
	staged := 0
	if prep != nil {
		staged = len(prep.stagedRoots)
	}
	return legacyContext{validateEnrichment: cfg.ValidateEnrichment, uploaderBackend: firstNonEmpty(cfg.UploaderBackend, "ddtest"),
		artifactSource: cfg.ArtifactSource, remoteArtifacts: cfg.RemoteArtifacts, freshnessSource: cfg.FreshnessSource,
		freshnessMode: cfg.FreshnessMode, allowCachedPayloadUploads: cfg.FreshnessDisabledExplicitly,
		bepFiles: cfg.BepJSONFiles, selectedSource: plan.selectedSource, strictCompleteness: plan.requireTestResults,
		completenessErrors: plan.completenessErrors, eligible: len(plan.eligible), cached: len(plan.cached),
		remoteOnly: len(plan.remoteOnly), skippedOutputs: skipped, missingOutputLabels: len(plan.missingOutputLabels),
		skippedTargets: len(plan.skippedTargets), stagingDir: cfg.ArtifactStagingDir, stagedTestlogsDirs: staged,
		selectedRemote: len(plan.selectedArtifactOutputs), stagedRemote: len(plan.stagedOutputs),
		remoteIgnored: len(plan.blockedLabels), testOutputsDirs: len(raw.outputs), reasonCode: final.code,
		reason: final.message, nextSteps: final.nextSteps}
}

// logInvocation is _log_invocation, without the runfiles snapshot that the
// Python uploader logs itself.
func (p *poster) logInvocation() {
	cfg := p.cfg
	mode := "upload"
	if cfg.DryRun {
		mode = "dry-run"
	}
	p.log.debugf("effective config: mode=%s workers=%d validate_enrichment=%s gzip=%s keep_payloads=%s filter_prefix=%s "+
		"fail_on_error=%s workspace=%s testlogs=%s freshness_source=%s freshness_mode=%s artifact_source=%s remote_artifacts=%s",
		mode, cfg.Workers, pyBool(cfg.ValidateEnrichment), pyBool(cfg.GzipPayloads), pyBool(cfg.KeepPayloads),
		pyBool(cfg.FilterPrefix), pyBool(cfg.FailOnError), cfg.Workspace, firstNonEmpty(cfg.TestlogsDir, "auto"),
		cfg.FreshnessSource, cfg.FreshnessMode, cfg.ArtifactSource, cfg.RemoteArtifacts)
}

func pyBool(value bool) string {
	if value {
		return "True"
	}
	return "False"
}

// logFreshnessMarkers is _log_legacy_freshness_markers.
func (p *poster) logFreshnessMarkers(plan freshnessPlan, skipped []string) {
	switch {
	case plan.selectedSource == "bep":
		p.log.infof("freshness filtering enabled: source=bep files=%d eligible_outputs=%d remote_only_outputs=%d",
			len(p.cfg.BepJSONFiles), len(plan.eligible), len(plan.remoteOnly))
	case plan.selectedSource == "execution_log":
		p.log.infof("freshness filtering enabled: source=execution_log")
	case p.cfg.FreshnessMode == "disabled":
		p.log.infof("freshness filtering disabled")
	}
	keys := map[string]bool{}
	for _, key := range skipped {
		keys[key] = true
	}
	for pr := range plan.cached {
		keys[pr.key] = true
	}
	if len(keys) > 0 {
		p.log.infof("freshness summary: skipped_cached_or_non_current_outputs=%d", len(keys))
	}
	for _, key := range sortedKeys(keys) {
		p.log.debugf("skipping cached or non-current test output: %s (freshness selection)", key)
	}
}

// logResults is the coordinator's _log_results.
func (p *poster) logResults(results []postResult) {
	for _, result := range results {
		p.log.debugf("task=%s type=%s terminal_status=%s attempts=%d retries=%d", result.ID, result.Kind, result.Status,
			result.Requests.Attempted, result.Requests.Retries)
		for _, code := range result.Warnings {
			if result.Status == "skipped" {
				p.log.debugf("task=%s file=%s warning_code=%s", result.ID, result.Path, code)
			} else {
				p.log.warnf("task=%s file=%s warning_code=%s", result.ID, result.Path, code)
			}
		}
		if result.Status == "failed" {
			p.log.errorf("task=%s file=%s failure_code=%s detail=%s", result.ID, result.Path,
				firstNonEmpty(result.Failure, "unknown"), firstNonEmpty(result.FailureMessage, "none"))
		}
	}
}

// logResultMarkers is _log_legacy_result_markers.
func (p *poster) logResultMarkers(results []postResult) {
	var tests []postResult
	for _, result := range results {
		if result.Kind == "test" && result.Status == "succeeded" {
			tests = append(tests, result)
		}
	}
	if p.cfg.ValidateEnrichment {
		prefix := ""
		if p.cfg.DryRun {
			prefix = "dry-run "
		}
		for _, result := range tests {
			p.log.debugf("%svalidated enriched test payload: %s", prefix, result.Path)
		}
	}
	if p.cfg.DryRun {
		p.log.infof("dry-run validated %d test payloads", len(tests))
	} else {
		p.log.infof("uploaded %d test payloads", len(tests))
	}
}

// emitReport is emit_report.
func (p *poster) emitReport(report aggregateReport, context legacyContext) {
	lines := report.humanLines()
	if p.log.enabled("INFO") {
		for _, line := range lines {
			p.log.line(line)
		}
	} else if report.exitCode != 0 {
		p.log.line(lines[0])
	}
	if p.cfg.ReportJSON == "" {
		return
	}
	if err := writeJSONReport(p.cfg.ReportJSON, report.schemaV1(context)); err != nil && p.log.enabled("WARN") {
		p.log.line(fmt.Sprintf("[dd-uploader] warning: failed to write uploader report: %T", errors.Unwrap(err)))
	}
}
