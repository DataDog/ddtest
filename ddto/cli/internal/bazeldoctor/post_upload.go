package bazeldoctor

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/DataDog/ddtest/ddto/cli/internal/bazelupload"
)

// This file is the uploader's coordinator with the ddtest backend,
// ddtest_backend.py and telemetry.py: every payload goes through
// bazelupload in this process.

// postResult is a FileResult, with the task's display path.
type postResult = bazelupload.FileResult

// uploadRun is what the coordinator returns: the results by task, the
// batch requests and the count of cancelled tasks.
type uploadRun struct {
	results   []postResult
	byID      map[string]postResult
	requests  []bazelupload.BatchRequest
	cancelled int
	workers   int
	exitCode  int
	warnings  []string
}

func passesPrefix(task postTask) bool {
	prefix := "coverage_"
	if task.kind == "test" {
		prefix = "span_events_"
	}
	return strings.HasPrefix(pathName(task.sourcePath), prefix)
}

func newRuntimeID() string {
	var value [16]byte
	_, _ = rand.Read(value[:])
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	text := hex.EncodeToString(value[:])
	return text[:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:]
}

// commonRoot is _common_root.
func commonRoot(paths []string) string {
	if len(paths) == 0 {
		return "/"
	}
	common := pathParts(paths[0])
	for _, path := range paths[1:] {
		parts := pathParts(path)
		length := 0
		for length < len(common) && length < len(parts) && common[length] == parts[length] {
			length++
		}
		common = common[:length]
	}
	root := "/"
	if len(common) > 1 {
		root = "/" + strings.Join(common[1:], "/")
	}
	if isDir(root) {
		return root
	}
	return pathParent(root)
}

// realPath is os.path.realpath.
func realPath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return resolvePath(path)
	}
	return resolved
}

// upload runs the tasks through bazelupload, as _run_native_upload does for
// tests and coverage and the Python workers did for telemetry.
func (p *poster) upload(disc postDiscovery) (uploadRun, error) {
	run := uploadRun{byID: map[string]postResult{}}
	var selected []postTask
	for _, task := range disc.tasks {
		if p.cfg.FilterPrefix && task.kind != "telemetry" && !passesPrefix(task) {
			run.results = append(run.results, postResult{ID: task.id, Path: task.displayPath,
				Kind: bazelupload.Kind(task.kind), Status: "skipped", Warnings: []string{"prefix_filter"}})
			continue
		}
		selected = append(selected, task)
	}
	telemetry, telemetryWarnings := p.telemetryPlan(selected)
	run.warnings = telemetryWarnings
	if len(selected) == 0 {
		return p.finishRun(run), nil
	}
	resources := p.request.Resources
	plan := bazelupload.Plan{Version: 1, RulesVersion: firstNonEmpty(p.cfg.RulesVersion, "unknown"),
		RuntimeID: newRuntimeID(), Contexts: resources.Contexts, ContextSelection: resources.ContextSelection}
	if plan.Contexts == nil {
		plan.Contexts = map[string]bazelupload.Object{}
	}
	if plan.ContextSelection == nil {
		plan.ContextSelection = &bazelupload.ContextSelection{}
	}
	realOutputs := map[string]string{}
	var outputs []string
	for _, task := range selected {
		if _, seen := realOutputs[task.outputDir]; task.outputDir != "" && !seen {
			realOutputs[task.outputDir] = ""
			outputs = append(outputs, task.outputDir)
		}
	}
	reals := make([]string, len(outputs))
	parallelFor(p.workers, len(outputs), func(i int) { reals[i] = realPath(outputs[i]) })
	for i, dir := range outputs {
		realOutputs[dir] = reals[i]
	}
	var paths []string
	taskWarnings := map[string][]string{}
	for _, task := range selected {
		source := bazelupload.Source{ID: task.id, Kind: bazelupload.Kind(task.kind)}
		path, output := realPath(task.sourcePath), ""
		if relative, ok := relativeTo(task.sourcePath, task.outputDir); task.outputDir != "" && ok && relative != "." {
			if realOutputs[task.outputDir] == "" {
				realOutputs[task.outputDir] = realPath(task.outputDir)
			}
			output = realOutputs[task.outputDir]
			path = output + "/" + relative
		}
		source.Path = path
		paths = append(paths, path)
		switch {
		case task.kind == "test" && output != "":
			// The run read the sidecar; the upload takes its metadata and
			// context, with the warnings that reading them gave.
			entry := p.sidecars[task.outputDir]
			if _, read := p.sidecars[task.outputDir]; !read {
				p.loadSidecars([]postOutput{{path: task.outputDir}})
				entry = p.sidecars[task.outputDir]
			}
			source.Metadata = entry.metadata
			name, warning := plan.ContextSelection.Choose(repoKey(entry.metadata))
			source.Context = name
			taskWarnings[task.id] = uniqueCodes([]string{entry.warning, warning})
		case task.kind == "test":
			name, warning := plan.ContextSelection.Choose("")
			if warning != "" {
				taskWarnings[task.id] = []string{warning}
			}
			source.Context = name
		case task.kind == "telemetry":
			source.Metadata = telemetry.metadata[task.id]
			source.Context = telemetry.contexts[task.id]
			source.Telemetry = telemetry.directives[task.id]
		}
		plan.Sources = append(plan.Sources, source)
	}
	codeowners := ""
	if resources.CodeownersFile != "" {
		codeowners = realPath(resources.CodeownersFile)
		paths = append(paths, codeowners)
	}
	root := commonRoot(paths)
	for i := range plan.Sources {
		plan.Sources[i].Path = relativePath(plan.Sources[i].Path, root)
		if plan.Sources[i].Output != "" {
			plan.Sources[i].Output = relativePath(plan.Sources[i].Output, root)
		}
	}
	if codeowners != "" {
		plan.Codeowners = relativePath(codeowners, root)
		plan.CodeownersDialect = "github"
		if pathName(pathParent(codeowners)) == ".gitlab" {
			plan.CodeownersDialect = "gitlab"
		}
	}
	options := bazelupload.Options{}
	if err := bazelupload.EnvironmentOptions(&options); err != nil {
		return run, failRun(2, "%v", err)
	}
	options.DryRun, options.KeepPayloads, options.Gzip = p.cfg.DryRun, p.cfg.KeepPayloads, p.cfg.GzipPayloads
	options.Batch = p.cfg.BatchTestPayloads
	// Only post-process prepares test sources by streaming; upload keeps
	// decoding them.
	options.Streaming = true
	if p.cfg.WorkersExplicit {
		options.Workers = p.cfg.Workers
	}
	if p.cfg.ValidateEnrichment {
		options.ExpectedTags = p.cfg.ExpectedEnrichedTags
		if len(options.ExpectedTags) == 0 {
			options.ExpectedTags = []string{"git.repository_url", "git.commit.sha", "bazel.target", "bazel.package"}
		}
	}
	p.log.debugf("ddtest upload: sources=%d root=%s", len(plan.Sources), root)
	report, err := bazelupload.Run(p.ctx, plan, root, options)
	if err != nil {
		for _, task := range selected {
			run.results = append(run.results, postResult{ID: task.id, Path: task.displayPath, Kind: bazelupload.Kind(task.kind),
				Status: "failed", Warnings: taskWarnings[task.id], Failure: "ddtest_upload_failed",
				FailureMessage: truncate("ddtest upload failed: "+err.Error(), 500)})
		}
		return p.finishRun(run), nil
	}
	byID := map[string]postTask{}
	for _, task := range selected {
		byID[task.id] = task
	}
	for _, result := range report.Sources {
		if result.Status == "cancelled" {
			run.cancelled++
			continue
		}
		task := byID[result.ID]
		result.Path = task.displayPath
		result.Warnings = uniqueCodes(append(append([]string{}, taskWarnings[result.ID]...), result.Warnings...))
		for i, id := range result.RequestIDs {
			result.RequestIDs[i] = "ddtest:" + id
		}
		run.results = append(run.results, result)
	}
	for _, request := range report.BatchRequests {
		request.ID = "ddtest:" + request.ID
		run.requests = append(run.requests, request)
	}
	run.workers = report.Workers
	if p.ctx.Err() != nil {
		run.exitCode = 130
	}
	return p.finishRun(run), nil
}

func (p *poster) finishRun(run uploadRun) uploadRun {
	for _, result := range run.results {
		run.byID[result.ID] = result
		if result.Status == "failed" && run.exitCode == 0 {
			run.exitCode = 1
		}
	}
	return run
}

func relativePath(path, root string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return relative
}

func truncate(text string, limit int) string {
	if len(text) > limit {
		return text[:limit]
	}
	return text
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// telemetryDirectives is TelemetryPlan, keyed by task: each telemetry
// source's directive, sidecar and context.
type telemetryDirectives struct {
	directives map[string]*bazelupload.TelemetryDirective
	metadata   map[string]bazelupload.Object
	contexts   map[string]string
}

type telemetrySource struct {
	task                                        postTask
	pathKey, service, language, runtimeID, kind string
	seqID                                       int64
	hasSeq                                      bool
}

type telemetryFacts struct {
	pathKey, service, runtime, environment string
	counts, distributions                  []any
}

// decodeJSONFile reads a JSON object as the uploader's strict_json_loads
// does, keeping numbers as written.
func decodeJSONFile(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte("\ufeff"))))
	decoder.UseNumber()
	var value map[string]any
	if decoder.Decode(&value) != nil || decoder.More() {
		return nil
	}
	return value
}

func jsonString(value any) string {
	text, _ := value.(string)
	return text
}

// telemetryPlan is build_telemetry_plan, with the sidecar and context that
// the Python worker selected for each telemetry source.
func (p *poster) telemetryPlan(tasks []postTask) (telemetryDirectives, []string) {
	plan := telemetryDirectives{directives: map[string]*bazelupload.TelemetryDirective{},
		metadata: map[string]bazelupload.Object{}, contexts: map[string]string{}}
	resources := p.request.Resources
	var warnings []string
	selection := resources.ContextSelection
	if selection == nil {
		selection = &bazelupload.ContextSelection{}
	}
	var telemetryTasks []postTask
	for _, task := range tasks {
		if task.kind == "telemetry" {
			telemetryTasks = append(telemetryTasks, task)
		}
	}
	sort.SliceStable(telemetryTasks, func(a, b int) bool { return telemetryTasks[a].sourcePath < telemetryTasks[b].sourcePath })
	var sources []telemetrySource
	for _, task := range telemetryTasks {
		if _, read := p.sidecars[task.outputDir]; !read && task.outputDir != "" {
			p.loadSidecars([]postOutput{{path: task.outputDir}})
		}
		metadata := p.sidecars[task.outputDir].metadata
		name, _ := selection.Choose(repoKey(metadata))
		plan.metadata[task.id], plan.contexts[task.id] = metadata, name
		payload := decodeJSONFile(task.sourcePath)
		if payload == nil {
			warnings = append(warnings, "telemetry_plan_input_invalid")
			continue
		}
		application, ok := payload["application"].(map[string]any)
		if !ok {
			continue
		}
		service := jsonString(application["service_name"])
		if reporting, err := reportingService(resources.Contexts[name], metadata); err != nil {
			warnings = append(warnings, "telemetry_reporting_service_invalid")
			continue
		} else if reporting != "" {
			service = reporting
		}
		source := telemetrySource{task: task, pathKey: resolvePath(task.sourcePath), service: service,
			language: jsonString(application["language_name"]), runtimeID: jsonString(payload["runtime_id"]),
			kind: jsonString(payload["request_type"])}
		if service == "" || source.language == "" || jsonString(payload["api_version"]) == "" || source.kind == "" {
			continue
		}
		if number, ok := payload["seq_id"].(json.Number); ok && !strings.ContainsAny(string(number), ".eE") {
			if value, err := number.Int64(); err == nil {
				source.seqID, source.hasSeq = value, true
			}
		}
		sources = append(sources, source)
	}
	suffix := ""
	if primary := resources.Contexts[resources.PrimaryContext]; primary != nil {
		suffix = strings.TrimSpace(firstNonEmpty(jsonString(primary["ci.provider.name"]), jsonString(primary["ci_provider_name"])))
	}
	directive := func(task postTask) *bazelupload.TelemetryDirective {
		if plan.directives[task.id] == nil {
			plan.directives[task.id] = &bazelupload.TelemetryDirective{ProviderSuffix: suffix}
		}
		return plan.directives[task.id]
	}
	if suffix != "" {
		for _, task := range telemetryTasks {
			directive(task)
		}
	}
	if len(sources) == 0 {
		return plan, uniqueCodes(warnings)
	}
	type stream struct{ service, language string }
	groups := map[stream][]telemetrySource{}
	for _, source := range sources {
		key := stream{source.service, source.language}
		groups[key] = append(groups[key], source)
	}
	factsPaths := append([]string{}, resources.TelemetryFactsPaths...)
	sort.Strings(factsPaths)
	grouped := map[stream][]telemetryFacts{}
	for _, path := range factsPaths {
		payload := decodeJSONFile(path)
		if payload == nil {
			warnings = append(warnings, "telemetry_facts_invalid")
			continue
		}
		facts := telemetryFacts{pathKey: resolvePath(path), service: jsonString(payload["service_name"]),
			runtime: jsonString(payload["runtime_name"]), environment: jsonString(payload["env"])}
		if facts.service == "" {
			warnings = append(warnings, "telemetry_facts_service_missing")
			continue
		}
		facts.counts, _ = payload["counts"].([]any)
		facts.distributions, _ = payload["distributions"].([]any)
		languages := map[string]bool{}
		for _, source := range sources {
			if source.service == facts.service {
				languages[source.language] = true
			}
		}
		if len(languages) == 0 {
			warnings = append(warnings, "telemetry_facts_anchor_missing")
			continue
		}
		language := ""
		if len(languages) == 1 {
			language = sortedKeys(languages)[0]
		} else if languages[facts.runtime] {
			language = facts.runtime
		} else {
			warnings = append(warnings, "telemetry_facts_language_ambiguous")
			continue
		}
		key := stream{facts.service, language}
		grouped[key] = append(grouped[key], facts)
	}
	keys := make([]stream, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(a, b int) bool {
		if keys[a].service != keys[b].service {
			return keys[a].service < keys[b].service
		}
		return keys[a].language < keys[b].language
	})
	timestamp := time.Now().Unix()
	for _, key := range keys {
		group := append([]telemetrySource{}, groups[key]...)
		if len(group) == 0 {
			continue
		}
		sort.SliceStable(group, func(a, b int) bool { return group[a].pathKey < group[b].pathKey })
		facts := append([]telemetryFacts{}, grouped[key]...)
		sort.SliceStable(facts, func(a, b int) bool { return facts[a].pathKey < facts[b].pathKey })
		environments := map[string]bool{}
		for _, entry := range facts {
			if entry.environment != "" {
				environments[entry.environment] = true
			}
		}
		if len(environments) > 1 {
			warnings = append(warnings, "telemetry_facts_env_conflict")
			continue
		}
		if len(environments) == 1 {
			environment := sortedKeys(environments)[0]
			for _, source := range group {
				directive(source.task).Environment = environment
			}
		}
		var counts, distributions []any
		for _, entry := range facts {
			counts = append(counts, entry.counts...)
			distributions = append(distributions, entry.distributions...)
		}
		messages := telemetryMessages(counts, distributions, timestamp)
		if len(messages) == 0 {
			continue
		}
		var order []string
		streams := map[string][]telemetrySource{}
		for _, source := range group {
			if _, seen := streams[source.runtimeID]; !seen {
				order = append(order, source.runtimeID)
			}
			streams[source.runtimeID] = append(streams[source.runtimeID], source)
		}
		var chosen []telemetrySource
		bestBatch, bestPath := false, ""
		for i, runtimeID := range order {
			items := streams[runtimeID]
			batch, path := false, ""
			for _, item := range items {
				batch = batch || item.kind == "message-batch"
			}
			path = streamBestPath(items)
			if i == 0 || batch && !bestBatch || batch == bestBatch && path > bestPath {
				chosen, bestBatch, bestPath = items, batch, path
			}
		}
		var batches []telemetrySource
		for _, source := range chosen {
			if source.kind == "message-batch" {
				batches = append(batches, source)
			}
		}
		candidates := chosen
		if len(batches) > 0 {
			candidates = batches
		}
		anchor := candidates[0]
		for _, source := range candidates[1:] {
			if source.pathKey > anchor.pathKey {
				anchor = source
			}
		}
		current := directive(anchor.task)
		if anchor.kind == "message-batch" {
			current.AppendMessages = messages
		} else {
			maxSeq := int64(0)
			for _, source := range chosen {
				if source.hasSeq && source.seqID > maxSeq {
					maxSeq = source.seqID
				}
			}
			current.SyntheticMessages, current.SyntheticSeqID, current.SyntheticTimestamp = messages, maxSeq+1, timestamp
		}
	}
	return plan, uniqueCodes(warnings)
}

// streamBestPath is _stream_best_path: the last message batch, or the last
// source.
func streamBestPath(items []telemetrySource) string {
	best, batch := "", ""
	for _, item := range items {
		best = max(best, item.pathKey)
		if item.kind == "message-batch" {
			batch = max(batch, item.pathKey)
		}
	}
	return firstNonEmpty(batch, best)
}

// telemetryMessages is _build_inner_messages.
func telemetryMessages(counts, distributions []any, timestamp int64) []any {
	var countSeries, distributionSeries []any
	for _, raw := range counts {
		fact, ok := raw.(map[string]any)
		if !ok || jsonString(fact["name"]) == "" {
			continue
		}
		tags, ok := fact["tags"].([]any)
		if !ok {
			tags = []any{}
		}
		countSeries = append(countSeries, map[string]any{"metric": jsonString(fact["name"]),
			"points": []any{[]any{timestamp, fact["value"]}}, "type": "count", "tags": tags, "common": true,
			"namespace": "civisibility"})
	}
	for _, raw := range distributions {
		fact, ok := raw.(map[string]any)
		if !ok || jsonString(fact["name"]) == "" {
			continue
		}
		tags, ok := fact["tags"].([]any)
		if !ok {
			tags = []any{}
		}
		distributionSeries = append(distributionSeries, map[string]any{"metric": jsonString(fact["name"]),
			"points": []any{fact["value"]}, "tags": tags, "common": true, "namespace": "civisibility"})
	}
	var messages []any
	if len(countSeries) > 0 {
		messages = append(messages, map[string]any{"request_type": "generate-metrics",
			"payload": map[string]any{"namespace": "civisibility", "series": countSeries}})
	}
	if len(distributionSeries) > 0 {
		messages = append(messages, map[string]any{"request_type": "distributions",
			"payload": map[string]any{"namespace": "", "series": distributionSeries}})
	}
	return messages
}

// repoKey is payload_repo_key.
func repoKey(metadata bazelupload.Object) string {
	return jsonString(metadata["bazel.test_optimization.repo_name"])
}

// reportingService is enrichment.reporting_service.
func reportingService(context, metadata bazelupload.Object) (string, error) {
	if metadata == nil {
		return "", nil
	}
	if _, declared := metadata["bazel.test_optimization.service_name"]; !declared {
		return "", nil
	}
	for _, identity := range [][2]string{{"bazel.test_optimization.repo_name", "topt.sync.repository_name"},
		{"bazel.test_optimization.service_name", "service.name"}, {"bazel.test_optimization.runtime_name", "runtime.name"}} {
		value := jsonString(metadata[identity[0]])
		if value == "" || context == nil || value != context[identity[1]] {
			return "", failRun(2, "reporting service identity mismatch: %s", identity[0])
		}
	}
	return jsonString(context["service.name"]), nil
}
