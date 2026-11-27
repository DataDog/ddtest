package bazelinspect

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"os"
	"runtime"
	"slices"
	"sort"
	"unicode/utf8"

	"github.com/DataDog/ddtest/ddto/cli/internal/pyjson"
)

// BepRequest lists the BEP JSON files whose freshness the rules_test_optimization
// doctor and uploader evaluate, after they resolved, deduplicated and checked
// them.
type BepRequest struct {
	Version int      `json:"schema_version"`
	Files   []string `json:"files"`
	// References asks for every artifact reference, which BEP artifact
	// staging reads; otherwise the report counts them and lists their labels.
	References bool `json:"references"`
}

// BepReport is what the doctor's _parse_bep_freshness computes from the files,
// except the identity of a document without a build UUID and the identity of
// each stageable carrier, which depend on the doctor's filesystem view.
//
// An execution is [document, label, configuration, run, shard, attempt,
// cached, status, output keys, carriers], each carrier [output key, fetch
// value, remote only]. A reference is [label index, kind, a, b, c, d, output
// key, flags]: kind 0 is a File object with a, b, c, d its uri, name, path
// and pathPrefix/name; kind 1 a string, in a; kind 2 any other value. Flags
// add 1 when cached, 2 when the fetch value is remote only, 4 for a
// test.outputs hint and 8 for a stageable carrier.
//
// Status is "fallback" when the files hold anything that the Python reads in
// its own way: invalid or unusual JSON, text other than ASCII where the
// evaluation compares it, or a record that makes the doctor fail. The doctor
// then evaluates them itself, so that its messages stay the same.
type BepReport struct {
	Version    int           `json:"schema_version"`
	Status     string        `json:"status"`
	Reason     string        `json:"reason,omitempty"`
	Documents  []BepDocument `json:"documents"`
	Labels     []string      `json:"labels"`
	References [][]any       `json:"references"`
	Count      int           `json:"reference_count"`
	RefLabels  []string      `json:"reference_labels"`
	Executions [][]any       `json:"executions"`
	Eligible   [][2]string   `json:"eligible_outputs"`
	Cached     [][2]string   `json:"cached_outputs"`
	RemoteOnly [][3]string   `json:"remote_only_outputs"`
	Missing    []string      `json:"missing_output_mappings"`
	Skipped    []string      `json:"skipped_targets"`
	Ambiguous  []string      `json:"ambiguous_outputs"`
	Conflicts  []string      `json:"ambiguous_labels"`
	Errors     [][2]string   `json:"completeness_errors"`
}

// BepDocument describes one requested file: Skip when an earlier file holds
// the same build, and the build UUID, empty when the file has none.
type BepDocument struct {
	UUID string `json:"uuid"`
	Skip bool   `json:"skip"`
}

var testStatuses = [...]string{"NO_STATUS", "PASSED", "FLAKY", "TIMEOUT", "FAILED", "INCOMPLETE", "REMOTE_FAILURE",
	"FAILED_TO_BUILD", "TOOL_HALTED_BEFORE_TESTING"}

// EvaluateBep reads the files, parsing their events in parallel.
func EvaluateBep(request BepRequest, workers int) BepReport {
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	report, err := evaluateBep(request.Files, request.References, workers)
	if err != nil {
		return BepReport{Version: 1, Status: "fallback", Reason: err.Error(), Documents: []BepDocument{},
			Labels: []string{}, References: [][]any{}, RefLabels: []string{}, Executions: [][]any{},
			Eligible: [][2]string{}, Cached: [][2]string{}, RemoteOnly: [][3]string{}, Missing: []string{},
			Skipped: []string{}, Ambiguous: []string{}, Conflicts: []string{}, Errors: [][2]string{}}
	}
	return report
}

// identity is a TestResult's (label, configuration, run, shard, attempt);
// the document stands for the invocation.
type identity struct {
	document             int
	label, configuration string
	run, shard, attempt  int64
}

type child struct {
	key, label string
}

type outputFacts struct {
	reference   []any
	fetch       string
	fetchRemote bool
	key         string
	keys        []string
	hint        bool
	stageable   bool
	remote      []string
}

type resultFacts struct {
	label   string
	result  bool
	status  string
	cached  bool
	outputs []outputFacts
	run     identity
}

type summaryFacts struct {
	label, configuration string
	present              bool
	counts               [4]int64
	valid                bool
}

// eventFacts holds what the freshness evaluation and the topology check read
// from one event.
type eventFacts struct {
	blank bool
	uuid  string

	key           string
	started       bool
	finished      bool
	last          bool
	children      []child
	topologyRun   *identity
	runCached     bool
	runMissing    bool
	summary       *summaryFacts
	skippedTarget string
	result        *resultFacts
}

func evaluateBep(files []string, withReferences bool, workers int) (BepReport, error) {
	labelIndex := map[string]int{}
	referenceLabels := map[string]bool{}
	report := BepReport{Version: 1, Status: "ok", Documents: []BepDocument{}, Labels: []string{}, References: [][]any{},
		Executions: [][]any{}, RemoteOnly: [][3]string{}, Errors: [][2]string{}}
	eligible, cached := map[[2]string]bool{}, map[[2]string]bool{}
	missing, skipped, results, ambiguous := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	fingerprints := map[string][32]byte{}
	for documentIndex, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return BepReport{}, errUnsupported
		}
		lines := splitLines(data)
		if len(lines) > 0 {
			lines[0] = bytes.TrimPrefix(lines[0], []byte("\xef\xbb\xbf"))
		}
		hash := sha256.New()
		for _, line := range lines {
			if !utf8.Valid(line) {
				return BepReport{}, errUnsupported
			}
			hash.Write(line)
			hash.Write([]byte{'\n'})
		}
		var fingerprint [32]byte
		copy(fingerprint[:], hash.Sum(nil))

		facts := make([]eventFacts, len(lines))
		failures := make([]error, len(lines))
		parallelFor(len(lines), workers, func(index int) {
			facts[index], failures[index] = readEvent(lines[index], documentIndex)
		})
		if err := errors.Join(failures...); err != nil {
			return BepReport{}, errUnsupported
		}

		document := BepDocument{}
		for _, event := range facts {
			if !event.blank && event.uuid != "" {
				document.UUID = event.uuid
				break
			}
		}
		if document.UUID != "" {
			if seen, ok := fingerprints[document.UUID]; ok {
				if seen != fingerprint {
					// The doctor fails on conflicting copies of one build.
					return BepReport{}, errUnsupported
				}
				document.Skip = true
				report.Documents = append(report.Documents, document)
				continue
			}
			fingerprints[document.UUID] = fingerprint
		}
		report.Documents = append(report.Documents, document)

		var events []*eventFacts
		for index := range facts {
			event := &facts[index]
			if event.blank {
				continue
			}
			events = append(events, event)
			if event.skippedTarget != "" {
				skipped[event.skippedTarget] = true
				continue
			}
			if event.result == nil {
				continue
			}
			result := event.result
			results[result.label] = true
			if !result.result {
				continue
			}
			mapped, remote := false, false
			fresh, cachedPairs := map[[2]string]bool{}, map[[2]string]bool{}
			type remoteCandidates struct {
				candidates []string
				hint       bool
				key        string
			}
			var remoteOnly []remoteCandidates
			for _, output := range result.outputs {
				if len(uniqueStrings(output.keys)) > 1 {
					for _, key := range output.keys {
						ambiguous[key] = true
					}
				}
				if output.key != "" {
					mapped = true
					pair := [2]string{result.label, output.key}
					if result.cached {
						cachedPairs[pair] = true
					} else {
						fresh[pair] = true
					}
				}
				report.Count++
				referenceLabels[result.label] = true
				if withReferences {
					index, ok := labelIndex[result.label]
					if !ok {
						index = len(report.Labels)
						labelIndex[result.label] = index
						report.Labels = append(report.Labels, result.label)
					}
					report.References = append(report.References, append([]any{index}, output.reference...))
				}
				if len(output.remote) > 0 && !result.cached {
					remoteOnly = append(remoteOnly, remoteCandidates{output.remote, output.hint, output.key})
				}
			}
			for _, item := range remoteOnly {
				if !item.hint && mapped {
					continue
				}
				for _, candidate := range item.candidates {
					remote = true
					report.RemoteOnly = append(report.RemoteOnly, [3]string{result.label, item.key, candidate})
				}
			}
			keys := map[string]bool{}
			for pair := range fresh {
				keys[pair[1]] = true
			}
			for pair := range cachedPairs {
				keys[pair[1]] = true
			}
			carriers := [][]any{}
			for _, output := range result.outputs {
				if output.key != "" && output.stageable {
					carriers = append(carriers, []any{output.key, output.fetch, output.fetchRemote})
				}
			}
			report.Executions = append(report.Executions, []any{documentIndex, result.label, result.run.configuration,
				result.run.run, result.run.shard, result.run.attempt, result.cached, result.status, sortedKeys(keys),
				carriers})
			for pair := range cachedPairs {
				cached[pair] = true
			}
			if !remote {
				for pair := range fresh {
					eligible[pair] = true
				}
			}
			if !mapped && !remote && !result.cached {
				missing[result.label] = true
			}
		}
		report.Errors = append(report.Errors, topologyErrors(events)...)
	}
	report.RefLabels = sortedKeys(referenceLabels)
	report.Eligible = sortedPairs(eligible)
	report.Cached = sortedPairs(cached)
	report.Missing = sortedKeys(missing)
	report.Skipped = sortedKeys(skipped)
	report.Ambiguous = sortedKeys(ambiguous)
	conflicts := map[string]bool{}
	for label := range skipped {
		if results[label] {
			conflicts[label] = true
		}
	}
	report.Conflicts = sortedKeys(conflicts)
	return report, nil
}

// splitLines is bytes.splitlines: lines end at \n, \r\n or \r.
func splitLines(data []byte) [][]byte {
	var lines [][]byte
	for len(data) > 0 {
		end := bytes.IndexAny(data, "\r\n")
		if end < 0 {
			lines = append(lines, data)
			break
		}
		lines = append(lines, data[:end])
		if data[end] == '\r' && end+1 < len(data) && data[end+1] == '\n' {
			end++
		}
		data = data[end+1:]
	}
	return lines
}

// blankLine is line.strip() == "" for ASCII whitespace; a line of other
// whitespace fails to parse and falls back.
func blankLine(line []byte) bool {
	for _, c := range line {
		switch c {
		case ' ', '\t', '\n', '\v', '\f', '\r', 0x1c, 0x1d, 0x1e, 0x1f:
		default:
			return false
		}
	}
	return true
}

// readEvent parses one line and reads its facts.
func readEvent(line []byte, document int) (eventFacts, error) {
	if blankLine(line) {
		return eventFacts{blank: true}, nil
	}
	value, err := parseJSON(line)
	if err != nil {
		return eventFacts{}, err
	}
	event, ok := value.(*jsonObject)
	if !ok {
		return eventFacts{}, errUnsupported
	}
	facts := eventFacts{}
	if started, ok := event.Lookup("started", nil).(*jsonObject); ok {
		facts.uuid, _ = started.Lookup("uuid", nil).(string)
	}
	rawID, ok := event.Lookup("id", nil).(*jsonObject)
	if !ok || len(rawID.Keys) == 0 {
		return eventFacts{}, errUnsupported
	}
	if err := readTopology(event, document, &facts); err != nil {
		return eventFacts{}, err
	}

	targetCompleted := coalesced(rawID, "targetCompleted", "target_completed", &jsonObject{})
	if completed, ok := targetCompleted.(*jsonObject); ok {
		if aborted, ok := event.Lookup("aborted", &jsonObject{}).(*jsonObject); ok {
			label, _ := completed.Lookup("label", nil).(string)
			if reason, ok := aborted.Lookup("reason", nil).(string); ok && label != "" && reason == "SKIPPED" {
				if !plainText(label) {
					return eventFacts{}, errUnsupported
				}
				facts.skippedTarget = label
				return facts, nil
			}
		}
	}
	resultID, ok := coalesced(rawID, "testResult", "test_result", &jsonObject{}).(*jsonObject)
	if !ok {
		return facts, nil
	}
	label, ok := resultID.Lookup("label", nil).(string)
	if !ok || label == "" {
		return facts, nil
	}
	if !plainText(label) {
		return eventFacts{}, errUnsupported
	}
	result := &resultFacts{label: label}
	facts.result = result
	body, ok := coalesced(event, "testResult", "test_result", &jsonObject{}).(*jsonObject)
	if !ok {
		return facts, nil
	}
	result.result = true
	switch status := body.Lookup("status", "NO_STATUS").(type) {
	case string:
		result.status = status
	case jsonNumber:
		index, isInt, err := pythonInt(status)
		if err != nil || !isInt || index < 0 || index >= int64(len(testStatuses)) {
			return eventFacts{}, errUnsupported
		}
		result.status = testStatuses[index]
	default:
		return eventFacts{}, errUnsupported
	}
	info := coalesced(body, "executionInfo", "execution_info", &jsonObject{})
	result.cached = pyjson.Truthy(coalesced(body, "cachedLocally", "cached_locally", false)) ||
		pyjson.Truthy(coalesced(info, "cachedRemotely", "cached_remotely", false))
	outputs, _ := coalesced(body, "testActionOutput", "test_action_output", []any{}).([]any)
	for _, output := range outputs {
		facts, err := readOutput(output, label, result.cached)
		if err != nil {
			return eventFacts{}, err
		}
		result.outputs = append(result.outputs, facts)
	}
	if result.run, err = executionIdentity(resultID, document); err != nil {
		return eventFacts{}, err
	}
	return facts, nil
}

// readOutput reads one TestResult output as the doctor's loop over
// testActionOutput does.
func readOutput(output any, label string, cached bool) (outputFacts, error) {
	if !plainOutput(output) {
		return outputFacts{}, errUnsupported
	}
	candidates := fileReferenceCandidates(output)
	canonical, err := canonicalOutputKeyCandidates(output, candidates)
	if err != nil {
		return outputFacts{}, err
	}
	name := stringField(output, "name")
	facts := outputFacts{}
	for _, candidate := range canonical {
		key, err := bepTestOutputKey(candidate, name)
		if err != nil {
			return outputFacts{}, err
		}
		if key != "" {
			facts.keys = append(facts.keys, key)
		}
	}
	if len(facts.keys) > 0 {
		facts.key = facts.keys[0]
	}
	if facts.stageable, err = stageableCarrierHint(output, candidates); err != nil {
		return outputFacts{}, err
	}
	for _, candidate := range candidates {
		hint, err := testOutputsArtifactHint(candidate)
		if err != nil {
			return outputFacts{}, err
		}
		facts.hint = facts.hint || hint
		if !cached && remoteOnlyReference(candidate) {
			facts.remote = append(facts.remote, candidate)
		}
	}
	facts.fetch = artifactFetchValue(output, candidates)
	facts.fetchRemote = remoteOnlyReference(facts.fetch)
	facts.stageable = facts.stageable && (!facts.fetchRemote || facts.key != "")
	flags := 0
	for bit, set := range []bool{cached, facts.fetchRemote, facts.hint, facts.stageable} {
		if set {
			flags |= 1 << bit
		}
	}
	switch typed := output.(type) {
	case *jsonObject:
		facts.reference = []any{0, stringField(typed, "uri"), stringField(typed, "name"), stringField(typed, "path"),
			pathPrefixNameCandidate(typed), facts.key, flags}
	case string:
		facts.reference = []any{1, typed, "", "", "", facts.key, flags}
	default:
		facts.reference = []any{2, "", "", "", "", facts.key, flags}
	}
	return facts, nil
}

// plainOutput reports whether the text that the evaluation reads from an
// output is plain.
func plainOutput(output any) bool {
	switch typed := output.(type) {
	case string:
		return plainText(typed)
	case *jsonObject:
		for _, key := range []string{"uri", "name", "path"} {
			if text, ok := typed.Lookup(key, nil).(string); ok && !plainText(text) {
				return false
			}
		}
		for _, key := range []string{"pathPrefix", "path_prefix"} {
			if parts, ok := typed.Lookup(key, nil).([]any); ok {
				for _, part := range parts {
					if text, ok := part.(string); ok && !plainText(text) {
						return false
					}
				}
			}
		}
	}
	return true
}

// executionIdentity is the doctor's _bep_execution_identity; an identity that
// makes the doctor fail is unsupported.
func executionIdentity(id *jsonObject, document int) (identity, error) {
	run := identity{document: document}
	run.label, _ = id.Lookup("label", "").(string)
	configuration, ok := id.Lookup("configuration", &jsonObject{}).(*jsonObject)
	if !ok {
		return identity{}, errUnsupported
	}
	if run.configuration, ok = configuration.Lookup("id", "").(string); !ok {
		return identity{}, errUnsupported
	}
	for _, field := range []struct {
		name     string
		fallback int64
		minimum  int64
		target   *int64
	}{{"run", 1, 1, &run.run}, {"shard", 0, 0, &run.shard}, {"attempt", 1, 1, &run.attempt}} {
		value, present := id.Get(field.name)
		if !present {
			*field.target = field.fallback
			continue
		}
		index, isInt, err := pythonInt(value)
		if err != nil || !isInt || index < field.minimum {
			return identity{}, errUnsupported
		}
		*field.target = index
	}
	return run, nil
}

// readTopology reads what bep_topology_errors reads from an event, from the
// fields that the doctor keeps for it.
func readTopology(event *jsonObject, document int, facts *eventFacts) error {
	rawID, _ := event.Get("id")
	normalized, err := normalizedEventID(rawID)
	if err != nil {
		return err
	}
	id, ok := normalized.(*jsonObject)
	if !ok || len(id.Keys) == 0 {
		return errUnsupported
	}
	if facts.key, err = canonicalJSON(id); err != nil {
		return err
	}
	_, hasStarted := id.Get("started")
	_, startedObject := event.Lookup("started", nil).(*jsonObject)
	facts.started = hasStarted && startedObject
	_, hasFinished := id.Get("build_finished")
	_, finishedObject := event.Lookup("finished", nil).(*jsonObject)
	facts.finished = hasFinished && finishedObject
	last, present := event.Get("lastMessage")
	if !present {
		last = event.Lookup("last_message", false)
	}
	facts.last = last == true

	children, ok := event.Lookup("children", []any{}).([]any)
	if !ok {
		return errUnsupported
	}
	for _, value := range children {
		normalized, err := normalizedEventID(value)
		if err != nil {
			return err
		}
		childID, ok := normalized.(*jsonObject)
		if !ok || len(childID.Keys) == 0 {
			return errUnsupported
		}
		key, err := canonicalJSON(childID)
		if err != nil {
			return err
		}
		testID, present := childID.Get("test_result")
		if !present {
			testID = childID.Lookup("test_summary", &jsonObject{})
		}
		label := ""
		if object, ok := testID.(*jsonObject); ok {
			var isText bool
			if label, isText = object.Lookup("label", "").(string); !isText {
				return errUnsupported
			}
		}
		facts.children = append(facts.children, child{key, label})
	}

	if resultID, ok := id.Lookup("test_result", nil).(*jsonObject); ok {
		if _, labelled := resultID.Lookup("label", nil).(string); labelled {
			run, err := executionIdentity(resultID, document)
			if err != nil {
				return err
			}
			facts.topologyRun = &run
			body, present := event.Get("testResult")
			if !present {
				body, present = event.Get("test_result")
			}
			if object, ok := body.(*jsonObject); ok && present {
				info := coalesced(object, "executionInfo", "execution_info", &jsonObject{})
				facts.runCached = coalesced(object, "cachedLocally", "cached_locally", false) == true ||
					coalesced(info, "cachedRemotely", "cached_remotely", false) == true
			} else {
				facts.runMissing = true
			}
		}
	}
	if summaryID, ok := id.Lookup("test_summary", nil).(*jsonObject); ok {
		if label, labelled := summaryID.Lookup("label", nil).(string); labelled {
			run, err := executionIdentity(summaryID, document)
			if err != nil {
				return err
			}
			summary := &summaryFacts{label: label, configuration: run.configuration}
			value, present := event.Get("testSummary")
			if !present {
				value = event.Lookup("test_summary", nil)
			}
			normalized, err := normalizedEventID(value)
			if err != nil {
				return err
			}
			if object, ok := normalized.(*jsonObject); ok {
				summary.present, summary.valid = true, true
				for i, name := range []string{"total_run_count", "run_count", "shard_count", "attempt_count"} {
					count, isInt, err := pythonInt(object.Lookup(name, jsonNumber("0")))
					if err != nil {
						return err
					}
					if !isInt || count < 0 {
						summary.valid = false
					}
					summary.counts[i] = count
				}
			}
			facts.summary = summary
		}
	}
	return nil
}

// topologyErrors is bep_topology_errors for one document's events.
func topologyErrors(events []*eventFacts) [][2]string {
	received := map[string]bool{}
	announced := map[string]string{}
	runs := map[identity]bool{}
	cachedRuns := map[identity]bool{}
	type summaryKey struct{ label, configuration string }
	summaries := map[summaryKey]*summaryFacts{}
	var errs [][2]string
	started, finished := false, false
	var last []int
	for index, event := range events {
		received[event.key] = true
		started = started || event.started
		finished = finished || event.finished
		if event.last {
			last = append(last, index)
		}
		for _, item := range event.children {
			announced[item.key] = item.label
		}
		if event.topologyRun != nil {
			run := *event.topologyRun
			runs[run] = true
			if event.runCached {
				cachedRuns[run] = true
			}
			if event.runMissing {
				errs = append(errs, [2]string{run.label, "BEP TestResult was aborted or has no result payload: " + run.label})
			}
		}
		if event.summary != nil {
			summaries[summaryKey{event.summary.label, event.summary.configuration}] = event.summary
		}
	}
	if !started || !finished || len(last) != 1 || last[0] != len(events)-1 {
		errs = append(errs, [2]string{"", "BEP stream is incomplete: require started, BuildFinished and a final last_message event"})
	}
	var missingKeys []string
	for key := range announced {
		if !received[key] {
			missingKeys = append(missingKeys, key)
		}
	}
	sort.Strings(missingKeys)
	for _, key := range missingKeys {
		errs = append(errs, [2]string{announced[key], "BEP announced event is missing: " + key})
	}
	grouped := map[summaryKey][]identity{}
	for run := range runs {
		key := summaryKey{run.label, run.configuration}
		grouped[key] = append(grouped[key], run)
	}
	var keys []summaryKey
	for key := range grouped {
		keys = append(keys, key)
	}
	for key := range summaries {
		if _, ok := grouped[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(a, b int) bool {
		if keys[a].label != keys[b].label {
			return keys[a].label < keys[b].label
		}
		return keys[a].configuration < keys[b].configuration
	})
	for _, key := range keys {
		identities := grouped[key]
		summary := summaries[key]
		if summary == nil || !summary.present {
			errs = append(errs, [2]string{key.label, "BEP TestSummary is missing: " + key.label})
			continue
		}
		if !summary.valid {
			errs = append(errs, [2]string{key.label, "invalid BEP TestSummary counts: " + key.label})
			continue
		}
		total, runCount, shards, attempts := summary.counts[0], max(1, summary.counts[1]), max(1, summary.counts[2]),
			summary.counts[3]
		type logicalRun struct{ run, shard int64 }
		logical := map[logicalRun]bool{}
		for _, run := range identities {
			logical[logicalRun{run.run, run.shard}] = true
		}
		valid := true
		allUnsharded := true
		for run := range logical {
			valid = valid && 1 <= run.run && run.run <= runCount && 1 <= run.shard && run.shard <= shards
			allUnsharded = allUnsharded && run.shard == 0
		}
		if shards == 1 && allUnsharded {
			valid = true
			for run := range logical {
				valid = valid && 1 <= run.run && run.run <= runCount
			}
		}
		collapsed := slices.ContainsFunc(identities, func(run identity) bool { return cachedRuns[run] })
		countMatches := total == int64(len(identities))
		if collapsed {
			countMatches = total >= int64(len(identities))
		}
		if !countMatches || int64(len(logical)) != runCount*shards || !valid {
			errs = append(errs, [2]string{key.label,
				"BEP TestSummary topology does not match received runs/shards/attempts: " + key.label})
		}
		attemptsByShard := map[int64]int64{}
		for _, run := range identities {
			attemptsByShard[run.shard]++
		}
		var received int64
		for _, count := range attemptsByShard {
			received = max(received, count)
		}
		attemptsMatch := received == attempts
		if collapsed {
			attemptsMatch = received <= attempts && attempts <= total
		}
		if attempts != 0 && !attemptsMatch {
			errs = append(errs, [2]string{key.label, "BEP TestSummary attempt count does not match received attempts: " + key.label})
		}
	}
	return errs
}

func uniqueStrings(values []string) []string {
	var unique []string
	for _, value := range values {
		if !slices.Contains(unique, value) {
			unique = append(unique, value)
		}
	}
	return unique
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedPairs(set map[[2]string]bool) [][2]string {
	pairs := make([][2]string, 0, len(set))
	for pair := range set {
		pairs = append(pairs, pair)
	}
	sort.Slice(pairs, func(a, b int) bool {
		if pairs[a][0] != pairs[b][0] {
			return pairs[a][0] < pairs[b][0]
		}
		return pairs[a][1] < pairs[b][1]
	})
	return pairs
}
