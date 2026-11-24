package bazeldoctor

import (
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/DataDog/ddtest/ddto/cli/internal/bazelinspect"
)

type pair struct{ label, key string }

// execution is BepTestExecution.
type execution struct {
	invocation           string
	label, configuration string
	run, shard, attempt  int64
	cached               bool
	status               string
	outputKeys           []string
	carriers             []pair
}

func (e execution) identity() identityKey {
	return identityKey{e.invocation, e.label, e.configuration, e.run, e.shard, e.attempt}
}

type identityKey struct {
	invocation, label, configuration string
	run, shard, attempt              int64
}

// reference is BepArtifactReference.
type reference struct {
	label, uri, name, path string
	candidates             []string
	fetchValue, outputKey  string
	cached, remoteOnly     bool
	testOutputsHint        bool
	stageableCarrier       bool
}

type remoteOutput struct{ label, outputKey, artifact, reason string }

// freshness is BepFreshness.
type freshness struct {
	eligible, cached  map[pair]bool
	remoteOnly        []remoteOutput
	missingMappings   map[string]bool
	references        []reference
	referenceCount    int
	referenceLabels   map[string]bool
	skipped           map[string]bool
	executions        []execution
	completenessError []pair // label, message
	ambiguousOutputs  map[string]bool
	ambiguousLabels   map[string]bool
	identifiedLabels  map[string]bool
}

// parseFreshness is _parse_bep_freshness. It returns nil, as Python does,
// when a BEP file is unavailable and that is not an error.
func (d *doctor) parseFreshness(files []string, unavailableIsError, references bool) *freshness {
	seen := map[string]bool{}
	var canonical []string
	missing := ""
	for _, file := range files {
		resolved := resolvePath(file)
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		info, err := os.Stat(file)
		if err != nil || !info.Mode().IsRegular() {
			missing = file
			break
		}
		canonical = append(canonical, resolved)
	}
	var result *freshness
	if len(canonical) > 0 {
		evaluation := bazelinspect.EvaluateBep(bazelinspect.BepRequest{Version: 1, Files: canonical,
			References: references}, d.workers)
		if evaluation.Status != "ok" {
			unsupported("BEP: " + evaluation.Reason)
		}
		result = d.freshnessFrom(canonical, evaluation, references)
	}
	if missing != "" {
		if !unavailableIsError {
			d.out.warn("BEP JSON file not found: " + missing + "; skipping BEP freshness " +
				"validation and preserving historical local output validation")
			return nil
		}
		d.out.fail("BEP JSON file not found: " + missing)
	}
	if result == nil {
		result = d.freshnessFrom(nil, bazelinspect.BepReport{}, references)
	}
	return result
}

// freshnessFrom builds BepFreshness from ddtest's evaluation, as the Python
// adapter _bep_freshness_with_ddtest does.
func (d *doctor) freshnessFrom(files []string, evaluation bazelinspect.BepReport, references bool) *freshness {
	f := &freshness{eligible: map[pair]bool{}, cached: map[pair]bool{}, missingMappings: map[string]bool{},
		referenceLabels: map[string]bool{}, skipped: map[string]bool{}, ambiguousOutputs: map[string]bool{},
		ambiguousLabels: map[string]bool{}, identifiedLabels: map[string]bool{}}
	invocations := make([]string, len(evaluation.Documents))
	for i, document := range evaluation.Documents {
		switch {
		case document.Skip:
		case document.UUID != "":
			invocations[i] = "uuid:" + document.UUID
		default:
			invocations[i] = "file:" + files[i]
		}
	}
	// Resolving a local carrier stats the workspace, execroot and output base
	// candidates; distinct carriers resolve in parallel.
	type carrierKey struct {
		value  string
		remote bool
	}
	identities := map[carrierKey]string{}
	var pending []carrierKey
	for _, record := range evaluation.Executions {
		for _, carrier := range record[9].([][]any) {
			key := carrierKey{carrier[1].(string), carrier[2].(bool)}
			if _, seen := identities[key]; !seen && !key.remote {
				identities[key] = ""
				pending = append(pending, key)
			}
		}
	}
	resolved := make([]string, len(pending))
	finished := make([]bool, len(pending))
	parallelFor(d.workers, len(pending), func(i int) {
		finished[i] = quietly(func() { resolved[i] = d.carrierIdentity(pending[i].value, false) })
	})
	for i, key := range pending {
		if finished[i] {
			identities[key] = resolved[i]
		} else {
			delete(identities, key)
		}
	}
	identity := func(value string, remote bool) string {
		if known, ok := identities[carrierKey{value, remote}]; ok && !remote {
			return known
		}
		return d.carrierIdentity(value, remote)
	}
	for _, record := range evaluation.Executions {
		e := execution{invocation: invocations[record[0].(int)], label: record[1].(string),
			configuration: record[2].(string), run: record[3].(int64), shard: record[4].(int64),
			attempt: record[5].(int64), cached: record[6].(bool), status: record[7].(string),
			outputKeys: record[8].([]string)}
		for _, carrier := range record[9].([][]any) {
			e.carriers = append(e.carriers, pair{carrier[0].(string),
				identity(carrier[1].(string), carrier[2].(bool))})
		}
		sort.Slice(e.carriers, func(a, b int) bool { return lessPair(e.carriers[a], e.carriers[b]) })
		f.executions = append(f.executions, e)
	}
	for _, record := range evaluation.References {
		ref := reference{label: evaluation.Labels[record[0].(int)], outputKey: record[6].(string)}
		first, second, third, fourth := record[2].(string), record[3].(string), record[4].(string), record[5].(string)
		switch record[1].(int) {
		case 0:
			for _, value := range []string{first, second, third, fourth} {
				if value != "" {
					ref.candidates = append(ref.candidates, value)
				}
			}
			ref.uri, ref.name, ref.path = first, second, third
			switch {
			case first != "":
				ref.fetchValue = first
			case third != "":
				ref.fetchValue = third
			case fourth != "":
				ref.fetchValue = fourth
			case len(ref.candidates) > 0:
				ref.fetchValue = ref.candidates[0]
			}
		case 1:
			ref.candidates = []string{first}
			ref.fetchValue = first
		}
		flags := record[7].(int)
		ref.cached, ref.remoteOnly = flags&1 != 0, flags&2 != 0
		ref.testOutputsHint, ref.stageableCarrier = flags&4 != 0, flags&8 != 0
		f.references = append(f.references, ref)
	}
	if !references {
		f.references = nil
	}
	f.referenceCount = evaluation.Count
	for _, label := range evaluation.RefLabels {
		f.referenceLabels[label] = true
	}
	for _, p := range evaluation.Eligible {
		f.eligible[pair{p[0], p[1]}] = true
	}
	for _, p := range evaluation.Cached {
		f.cached[pair{p[0], p[1]}] = true
	}
	for _, r := range evaluation.RemoteOnly {
		f.remoteOnly = append(f.remoteOnly, remoteOutput{r[0], r[1], r[2], "remote_only"})
	}
	for _, label := range evaluation.Missing {
		f.missingMappings[label] = true
	}
	for _, label := range evaluation.Skipped {
		f.skipped[label] = true
	}
	for _, e := range evaluation.Errors {
		f.completenessError = append(f.completenessError, pair{e[0], e[1]})
	}
	// BepFreshness.__init__.
	for key := range ambiguousExecutionOutputs(f.executions) {
		f.ambiguousOutputs[key] = true
	}
	for p := range f.eligible {
		if f.cached[p] {
			f.ambiguousOutputs[p.key] = true
		}
	}
	for _, key := range evaluation.Ambiguous {
		f.ambiguousOutputs[key] = true
	}
	for _, label := range evaluation.Conflicts {
		f.ambiguousLabels[label] = true
	}
	for _, e := range f.executions {
		if f.ambiguousLabels[e.label] {
			for _, key := range e.outputKeys {
				f.ambiguousOutputs[key] = true
			}
		}
	}
	return f
}

func lessPair(a, b pair) bool {
	if a.label != b.label {
		return a.label < b.label
	}
	return a.key < b.key
}

func sortedPairs(set map[pair]bool) []pair {
	pairs := make([]pair, 0, len(set))
	for p := range set {
		pairs = append(pairs, p)
	}
	sort.Slice(pairs, func(a, b int) bool { return lessPair(pairs[a], pairs[b]) })
	return pairs
}

func labelsOf(set map[pair]bool) map[string]bool {
	labels := map[string]bool{}
	for p := range set {
		labels[p.label] = true
	}
	return labels
}

// ambiguousExecutionOutputs is ambiguous_execution_outputs.
func ambiguousExecutionOutputs(executions []execution) map[string]bool {
	byKey := map[string]map[identityKey]bool{}
	byIdentity := map[identityKey][]execution{}
	ambiguous := map[string]bool{}
	for _, e := range executions {
		id := e.identity()
		byIdentity[id] = append(byIdentity[id], e)
		for _, key := range e.outputKeys {
			if byKey[key] == nil {
				byKey[key] = map[identityKey]bool{}
			}
			byKey[key][id] = true
		}
		carriers := map[string]map[string]bool{}
		for _, c := range e.carriers {
			if carriers[c.label] == nil {
				carriers[c.label] = map[string]bool{}
			}
			carriers[c.label][c.key] = true
		}
		for key, values := range carriers {
			if len(values) > 1 {
				ambiguous[key] = true
			}
		}
	}
	for key, identities := range byKey {
		if len(identities) > 1 {
			ambiguous[key] = true
		}
	}
	for _, records := range byIdentity {
		// A set of frozen dataclasses: equal records count once.
		distinct := map[string]bool{}
		for _, r := range records {
			distinct[executionFingerprint(r)] = true
		}
		if len(distinct) > 1 {
			for _, r := range records {
				for _, key := range r.outputKeys {
					ambiguous[key] = true
				}
			}
		}
	}
	return ambiguous
}

func executionFingerprint(e execution) string {
	keys := append([]string{}, e.outputKeys...)
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteString(strconv.FormatBool(e.cached) + "\x00" + e.status + "\x00" + strings.Join(keys, "\x01"))
	for _, c := range e.carriers {
		builder.WriteString("\x00" + c.label + "\x01" + c.key)
	}
	return builder.String()
}

// exclusivelyCachedLabels is exclusively_cached_labels.
func exclusivelyCachedLabels(executions []execution) map[string]bool {
	cached, fresh := map[string]bool{}, map[string]bool{}
	for _, e := range executions {
		if e.cached {
			cached[e.label] = true
		} else {
			fresh[e.label] = true
		}
	}
	for label := range fresh {
		delete(cached, label)
	}
	return cached
}

// firstCompletenessError is first_bep_completeness_error.
func (f *freshness) firstCompletenessError(labels map[string]bool) string {
	for _, e := range f.completenessError {
		if e.label == "" || labels[e.label] {
			return e.key
		}
	}
	return ""
}

type shardKey struct {
	invocation, label, configuration string
	run, shard                       int64
}

var retryableStatuses = map[string]bool{"TIMEOUT": true, "FAILED": true, "INCOMPLETE": true, "REMOTE_FAILURE": true,
	"FAILED_TO_BUILD": true, "TOOL_HALTED_BEFORE_TESTING": true}

// missingTestShards is missing_test_shards: the first unproven fresh shard.
func missingTestShards(executions []execution, proven map[pair]bool, selected map[string]bool, empty map[pair]bool) (shardKey, bool) {
	shards := map[shardKey][]execution{}
	for _, e := range executions {
		if selected[e.label] {
			key := shardKey{e.invocation, e.label, e.configuration, e.run, e.shard}
			shards[key] = append(shards[key], e)
		}
	}
	keys := make([]shardKey, 0, len(shards))
	for key := range shards {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(a, b int) bool {
		x, y := keys[a], keys[b]
		switch {
		case x.invocation != y.invocation:
			return x.invocation < y.invocation
		case x.label != y.label:
			return x.label < y.label
		case x.configuration != y.configuration:
			return x.configuration < y.configuration
		case x.run != y.run:
			return x.run < y.run
		}
		return x.shard < y.shard
	})
	for _, key := range keys {
		var fresh []execution
		for _, attempt := range shards[key] {
			if !attempt.cached {
				fresh = append(fresh, attempt)
			}
		}
		provenAttempts := map[int64]bool{}
		for _, attempt := range fresh {
			for _, outputKey := range attempt.outputKeys {
				p := pair{key.label, outputKey}
				if proven[p] || attempt.status == "PASSED" && empty[p] {
					provenAttempts[attempt.attempt] = true
					break
				}
			}
		}
		for _, attempt := range fresh {
			if provenAttempts[attempt.attempt] {
				continue
			}
			later := false
			for number := range provenAttempts {
				later = later || number > attempt.attempt
			}
			if retryableStatuses[attempt.status] && later {
				continue
			}
			return key, true
		}
	}
	return shardKey{}, false
}

// carrierIdentity is _bep_carrier_identity.
func (d *doctor) carrierIdentity(fetchValue string, remoteOnly bool) string {
	if remoteOnly {
		return "remote:" + fetchValue
	}
	if resolved, ok := d.resolveLocalArtifact(fetchValue); ok {
		return "local:" + resolved
	}
	path := localArtifactPath(fetchValue)
	if !filepath.IsAbs(path) {
		path = filepath.Join(d.workspace, path)
	}
	return "local:" + resolvePath(path)
}

// outputIndex is _BepOutputIndex.
type outputIndex map[string]map[string]bool

func newOutputIndex(pairs map[pair]bool) outputIndex {
	index := outputIndex{}
	for p := range pairs {
		if index[p.key] == nil {
			index[p.key] = map[string]bool{}
		}
		index[p.key][p.label] = true
	}
	return index
}

func (index outputIndex) labelsFor(outputDir, explicitKey string) map[string]bool {
	if explicitKey != "" {
		return index[explicitKey]
	}
	path := strings.TrimRight(bazelinspect.CanonicalOutputDirectoryKey(outputDir), "/")
	labels := map[string]bool{}
	for label := range index[path] {
		labels[label] = true
	}
	for offset := 0; offset < len(path); offset++ {
		if path[offset] == '/' {
			for label := range index[path[offset+1:]] {
				labels[label] = true
			}
		}
	}
	return labels
}

// displayArtifactReference is _display_artifact_reference: an HTTP URL
// without its credentials, query and fragment.
func displayArtifactReference(value string) string {
	if value != "" && value[0] <= ' ' {
		// urlsplit strips leading control characters and spaces.
		unsupported("artifact reference with leading whitespace")
	}
	scheme, rest, hasScheme := strings.Cut(value, "://")
	lowered := strings.ToLower(scheme)
	if !hasScheme || lowered != "http" && lowered != "https" || !plainASCII(value) {
		if !plainASCII(value) && (strings.HasPrefix(strings.ToLower(value), "http://") ||
			strings.HasPrefix(strings.ToLower(value), "https://")) {
			unsupported("non-ASCII HTTP artifact reference")
		}
		return value
	}
	cleaned := strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(rest)
	end := len(cleaned)
	if index := strings.IndexAny(cleaned, "/?#"); index >= 0 {
		end = index
	}
	netloc, path := cleaned[:end], cleaned[end:]
	if strings.Count(netloc, "[") != strings.Count(netloc, "]") || strings.ContainsAny(netloc, "[]") {
		// urlsplit validates bracketed hosts; leave those to Python.
		unsupported("bracketed host in an HTTP artifact reference")
	}
	path, _, _ = strings.Cut(path, "#")
	path, _, _ = strings.Cut(path, "?")
	hostPort := netloc[strings.LastIndex(netloc, "@")+1:]
	host, port, hasPort := strings.Cut(hostPort, ":")
	host = strings.ToLower(host)
	if host == "" {
		return lowered + "://redacted-invalid-url"
	}
	if hasPort && port != "" {
		number, err := strconv.Atoi(port)
		if err == nil && number >= 0 && number <= 65535 && strings.TrimLeft(port, "0123456789") == "" {
			host = net.JoinHostPort(host, strconv.Itoa(number))
			if strings.HasPrefix(host, "[") {
				unsupported("IPv6 host")
			}
		}
	}
	if path != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return lowered + "://" + host + path
}

func plainASCII(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] >= 0x80 {
			return false
		}
	}
	return true
}
