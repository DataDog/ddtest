package bazelupload

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"
)

// Options are the command's settings for one invocation.
type Options struct {
	DryRun       bool
	KeepPayloads bool
	Batch        bool
	Gzip         bool
	KeepAlive    bool
	ExpectedTags []string
	Workers      int
	APIKey       string
	Site         string
	AgentURL     string
	AgentlessURL string
}

type invocation struct {
	root        *os.Root
	plan        Plan
	contexts    map[string]flatMetadata
	options     Options
	urls        endpoints
	apiKey      string
	schema      *schemaNode
	send        sender
	results     []FileResult
	batchMu     sync.Mutex
	batches     []BatchRequest
	files       []os.FileInfo
	directories *directories
	// held is each source's directory, from its read to its cleanup.
	held []*directory
}

type worker struct {
	*invocation
	owners     ownerLookup
	compressor *gzip.Writer
	encoder    *jsonEncoder
}

// upload processes the plan's sources, which root contains, and reports each
// one in plan order. An error means that no source was processed.
func upload(ctx context.Context, root *os.Root, plan Plan, options Options) (Report, error) {
	start := time.Now()
	urls, err := resolveEndpoints(options)
	if err != nil {
		return Report{}, err
	}
	if urls.agentless && !options.DryRun && options.APIKey == "" {
		return Report{}, fmt.Errorf("DD_API_KEY is required for agentless uploads")
	}
	if options.Workers < 0 {
		return Report{}, fmt.Errorf("workers must be nonnegative")
	}
	if options.Workers == 0 {
		options.Workers = runtime.GOMAXPROCS(0)
	}

	rules, err := readOwners(root, plan.Codeowners, plan.CodeownersDialect)
	if err != nil {
		return Report{}, err
	}
	schema, err := compileSchema()
	if err != nil {
		return Report{}, fmt.Errorf("compile payload schema: %w", err)
	}
	contexts := make(map[string]flatMetadata, len(plan.Contexts))
	for name, values := range plan.Contexts {
		contexts[name] = flatten(values, true)
	}
	workers := min(options.Workers, len(plan.Sources))
	results := make([]FileResult, len(plan.Sources))
	for index, source := range plan.Sources {
		results[index] = FileResult{ID: source.ID, Path: filepath.ToSlash(source.Path), Kind: source.Kind, Status: statusCancelled}
	}
	inv := &invocation{
		root: root, plan: plan, contexts: contexts, options: options, urls: urls,
		schema: schema, send: newSender(options, senders(workers)), results: results,
		files: make([]os.FileInfo, len(results)), directories: newDirectories(root), held: make([]*directory, len(results)),
	}
	if urls.agentless {
		inv.apiKey = options.APIKey
	}
	defer inv.send.client.CloseIdleConnections()
	inv.run(ctx, workers, func() *worker {
		return &worker{
			invocation: inv,
			owners:     ownerLookup{rules: rules, workspace: root.Name(), cache: make(map[[2]string]string)},
			compressor: gzip.NewWriter(io.Discard),
			encoder:    newJSONEncoder(),
		}
	})

	report := Report{Version: 1, Mode: "upload", Workers: workers, Sources: results, BatchRequests: inv.batches,
		ElapsedSeconds: time.Since(start).Seconds()}
	if options.DryRun {
		report.Mode = "dry-run"
	}
	report.aggregate()
	return report, nil
}

// prepared is a source after its CPU work: read, decoded, enriched and
// validated, with the requests that still need sending.
type prepared struct {
	ok        bool
	test      preparedTest
	candidate batchCandidate
	requests  []outbound
}

// A unit is one sending task: a test source, a batch of them, or a coverage
// or telemetry source. It takes over the prepared data from the sources'
// slots, so whoever sends it can let the data go.
type unit struct {
	index    int
	batch    []batchEntry
	test     preparedTest
	requests []outbound
}

// senders is how many requests are in flight at once. A sender mostly waits
// for the intake, so there are more senders than workers.
func senders(workers int) int {
	return max(32, 2*workers)
}

// decodedSources bounds the sources that are decoded at once: those that the
// workers prepare, those that wait in open batches, and those whose requests
// the senders encode. A source leaves the bound once its requests are
// encoded; then it holds only their compressed bodies.
func decodedSources(workers int, batch bool) int {
	if !batch {
		return 4 * workers
	}
	return 8*workers + 2*batchSources
}

// run prepares sources on every worker and sends their requests on more
// senders, so neither the batches nor the network bound the parallelism:
//
//   - the workers prepare sources, taken in plan order;
//   - one assembler takes them as they are ready and groups compatible test
//     sources into batches of up to batchSources and a request's size;
//   - the senders deliver each unit and clean up its sources.
//
// Taking sources as they are ready keeps a slow source, such as one over the
// request limit, from holding up those behind it.
func (inv *invocation) run(ctx context.Context, workers int, newWorker func() *worker) {
	if workers == 0 {
		return
	}
	slots := make([]prepared, len(inv.plan.Sources))
	sending := senders(workers)
	bound := decodedSources(workers, inv.options.Batch)
	tokens := make(chan struct{}, bound)
	release := func(count int) {
		for range count {
			<-tokens
		}
	}
	queue, ready := make(chan int), make(chan int, workers)
	go func() {
		defer close(queue)
		for index := range slots {
			select {
			case tokens <- struct{}{}:
			case <-ctx.Done():
				return
			}
			select {
			case queue <- index:
			case <-ctx.Done():
				release(1)
				return
			}
		}
	}()
	var preparing, sendingDone sync.WaitGroup
	for range workers {
		preparing.Go(func() {
			work := newWorker()
			for index := range queue {
				if ctx.Err() == nil {
					work.prepare(index, &slots[index])
				}
				ready <- index
			}
		})
	}
	go func() {
		preparing.Wait()
		close(ready)
	}()
	units := make(chan unit)
	for range sending {
		sendingDone.Go(func() {
			work := newWorker()
			for task := range units {
				count, released := max(1, len(task.batch)), false
				encoded := func() {
					if !released {
						released = true
						release(count)
					}
				}
				if ctx.Err() == nil {
					work.deliver(ctx, &task, encoded)
				}
				for _, entry := range task.batch {
					inv.closeDirectory(entry.index)
				}
				if task.batch == nil {
					inv.closeDirectory(task.index)
				}
				encoded()
			}
		})
	}
	inv.assemble(slots, ready, units, release, bound/2, 2*workers)
	close(units)
	sendingDone.Wait()
}

// openBatch is a batch that can still take sources. last is the arrival that
// last extended it.
type openBatch struct {
	entries []batchEntry
	size    int
	last    int
}

// assemble turns prepared sources into units as they are ready. A source
// joins the open batch of its context and batch key, which leaves once it
// holds batchSources sources, another source would take it past the request
// limit, or window arrivals pass without one for it: sources arrive about in
// plan order, so its run of compatible sources has ended. When the open
// batches hold more than limit sources, the one extended longest ago leaves,
// so that waiting sources never starve the workers.
func (inv *invocation) assemble(slots []prepared, ready <-chan int, units chan<- unit, release func(int), limit, window int) {
	open := make(map[string]*openBatch)
	held, arrivals := 0, 0
	oldest := func() string {
		found := ""
		for key, batch := range open {
			if found == "" || batch.last < open[found].last {
				found = key
			}
		}
		return found
	}
	flush := func(key string) {
		batch := open[key]
		delete(open, key)
		held -= len(batch.entries)
		units <- unit{batch: batch.entries}
	}
	for index := range ready {
		slot := &slots[index]
		if !slot.ok {
			inv.closeDirectory(index)
			release(1)
			continue
		}
		candidate, test, requests := slot.candidate, slot.test, slot.requests
		*slot = prepared{}
		if candidate.key == "" {
			units <- unit{index: index, test: test, requests: requests}
			continue
		}
		key := inv.plan.Sources[index].Context + "\x00" + candidate.key
		if batch := open[key]; batch != nil && batch.size+candidate.size > testRequestLimit {
			flush(key)
		}
		batch := open[key]
		if batch == nil {
			batch = &openBatch{}
			open[key] = batch
		}
		arrivals++
		batch.entries = append(batch.entries, batchEntry{index: index, test: test, candidate: candidate})
		batch.size += candidate.size
		batch.last = arrivals
		held++
		if len(batch.entries) == batchSources {
			flush(key)
		}
		for key, batch := range open {
			if arrivals-batch.last > window {
				flush(key)
			}
		}
		for held > limit {
			flush(oldest())
		}
	}
	for key := range open {
		flush(key)
	}
}

// prepare does a source's CPU work. A source without requests to send keeps
// the status that its preparation reported.
func (work *worker) prepare(index int, slot *prepared) {
	source := work.plan.Sources[index]
	if source.Kind != Test {
		slot.requests, slot.ok = work.prepareOther(index)
		return
	}
	test, ok := work.readTest(index)
	if !ok {
		return
	}
	slot.test, slot.ok = test, true
	if work.options.Batch && source.Context != "" && len(work.results[index].Warnings) == 0 {
		slot.candidate = prepareBatch(test)
	}
}

// deliver sends the unit's requests and calls encoded once its decoded sources
// can go.
func (work *worker) deliver(ctx context.Context, task *unit, encoded func()) {
	switch {
	case task.batch != nil:
		work.flushBatch(ctx, task.batch, encoded)
	case work.plan.Sources[task.index].Kind == Test:
		work.uploadTest(ctx, task.index, &task.test, encoded)
	default:
		encoded()
		work.sendOther(ctx, task.index, task.requests)
	}
}

// directory returns the source's open directory, which it holds from the
// first use to closeDirectory, and the source's name in it.
func (work *worker) directory(index int) (*os.Root, string, error) {
	source := work.plan.Sources[index].Path
	if work.held[index] == nil {
		work.held[index] = work.directories.acquire(filepath.Dir(source))
	}
	if err := work.held[index].err; err != nil {
		return nil, "", err
	}
	return work.held[index].root, filepath.Base(source), nil
}

func (inv *invocation) closeDirectory(index int) {
	inv.directories.release(inv.held[index])
	inv.held[index] = nil
}

func (work *worker) read(index int) ([]byte, error) {
	dir, name, err := work.directory(index)
	if err != nil {
		return nil, err
	}
	info, err := dir.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("expected a regular payload file")
	}
	handle, err := dir.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = handle.Close() }()
	opened, err := handle.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("payload changed during open")
	}
	work.files[index] = opened
	var data bytes.Buffer
	data.Grow(int(opened.Size()) + bytes.MinRead)
	if _, err := data.ReadFrom(handle); err != nil {
		return nil, err
	}
	work.results[index].SourceBytes = data.Len()
	return data.Bytes(), nil
}

// loadTest reads, decodes and enriches a test source. It returns the failure
// code of an error; a payload without events has neither.
func (work *worker) loadTest(index int) (Object, []any, int, string, error) {
	source := work.plan.Sources[index]
	if !checkSuffix(source, ".json") {
		return nil, nil, 0, "unsupported_test_payload_format", fmt.Errorf("test payloads must be JSON")
	}
	raw, err := work.read(index)
	if err != nil {
		return nil, nil, 0, "test_payload_read_failed", err
	}
	payload, err := decodeObject(raw)
	if err != nil {
		return nil, nil, 0, "invalid_test_payload", err
	}
	events, _ := payload["events"].([]any)
	if len(events) == 0 {
		return payload, nil, len(raw), "", nil
	}
	if err := enrich(payload, source, work.plan, work.contexts[source.Context], &work.owners); err != nil {
		return nil, nil, 0, "reporting_service_invalid", err
	}
	return payload, events, len(raw), "", nil
}

// readTest prepares a test source for its requests. It reads the source again
// when a rejected batch falls back to its sources, so it sets the result's
// counts rather than adding to them.
func (work *worker) readTest(index int) (preparedTest, bool) {
	source, result := work.plan.Sources[index], &work.results[index]
	payload, events, size, code, err := work.loadTest(index)
	if err != nil {
		result.fail(code, err)
		return preparedTest{}, false
	}
	if len(events) == 0 {
		result.Status = statusSkipped
		result.warn("test_payload_without_events")
		return preparedTest{}, false
	}
	result.Events = len(events)
	result.TestEvents = 0
	for _, value := range events {
		event := object(value)
		if testSpan(stringValue(event, "type"), object(event["content"])) {
			result.TestEvents++
		}
	}
	result.EmptyTestSession = result.TestEvents == 0 && emptyTestSession(events)
	if !work.schema.accepts(payload) {
		result.warn("schema_validation_failed")
	}
	if err := validateEnrichment(events, work.options.ExpectedTags); err != nil {
		result.fail("enrichment_tags_missing", err)
		return preparedTest{}, false
	}
	evp := ""
	if !work.urls.agentless {
		evp = "citestcycle-intake"
	}
	envelope := withoutEvents(payload)
	test := preparedTest{envelope: envelope, events: events,
		headers: requestHeaders(work.plan, object(object(envelope["metadata"])["*"]), work.apiKey, evp)}
	if work.options.Batch && source.Context != "" && len(result.Warnings) == 0 {
		test.estimate = size + len(events)*(enrichmentBytes(work.contexts[source.Context], flatten(source.Metadata, false))+64)
		return test, true
	}
	if !work.chunk(index, &test) {
		return preparedTest{}, false
	}
	return test, true
}

// chunk splits the source's events into its own requests.
func (work *worker) chunk(index int, test *preparedTest) bool {
	chunks, err := prepareChunks(test.envelope, test.events, testRequestLimit, work.results[index].SourceBytes, work.encoder)
	if err != nil {
		work.results[index].fail("single_event_exceeds_payload_limit", err)
		return false
	}
	test.chunks = chunks
	return true
}

func (work *worker) testRequest(body []byte, headers http.Header) outbound {
	return outbound{body: body, url: work.urls.test, headers: headers, gzip: work.options.Gzip}
}

// uploadTest sends a test source on its own. Once its requests are encoded it
// drops the decoded source, which would otherwise stay in memory while the
// requests wait for the intake; the source stays on disk until it is
// delivered, so a partial failure reads it again for its replay.
func (work *worker) uploadTest(ctx context.Context, index int, test *preparedTest, encoded func()) {
	result := &work.results[index]
	if test.chunks == nil && !work.chunk(index, test) {
		return
	}
	chunks, headers := test.chunks, test.headers
	*test = preparedTest{}
	encoded()
	result.ChunksCreated = len(chunks)
	var failed []testChunk
	for i := range chunks {
		delivery := work.send.post(ctx, work.testRequest(chunks[i].body, headers), work.compressor)
		chunks[i].body = nil
		work.recordTest([]int{index}, delivery)
		if delivery.Err != nil {
			work.failDelivery(result, delivery)
			result.ChunksFailed++
			failed = append(failed, chunks[i])
		}
		if delivery.Success {
			result.ChunksUploaded++
		}
	}
	if result.ChunksFailed == 0 {
		work.finish(index)
	} else if result.ChunksUploaded > 0 && !work.options.KeepPayloads {
		work.retainFailedChunks(index, failed)
	}
}

// retainFailedChunks replaces the source with the events of its failed
// chunks, so that a retry sends only those.
func (work *worker) retainFailedChunks(index int, failed []testChunk) {
	result := &work.results[index]
	payload, events, _, _, err := work.loadTest(index)
	if err == nil && len(events) == 0 {
		err = errors.New("the source has no events left")
	}
	var body []byte
	if err == nil {
		var rejected []any
		for _, chunk := range failed {
			rejected = append(rejected, events[chunk.first:chunk.end]...)
		}
		envelope := withoutEvents(payload)
		envelope["events"] = rejected
		body, err = encodeJSON(envelope)
	}
	if err != nil || work.replace(index, body) != nil {
		result.warn("failed_test_chunks_persist_failed")
	}
}

// prepareOther reads a coverage or telemetry source and prepares its requests.
func (work *worker) prepareOther(index int) ([]outbound, bool) {
	source, result := work.plan.Sources[index], &work.results[index]
	raw, err := work.read(index)
	if err != nil {
		result.fail(string(source.Kind)+"_payload_read_failed", err)
		return nil, false
	}
	var requests []outbound
	if source.Kind == Coverage {
		if !checkSuffix(source, ".json", ".msgpack") {
			result.fail("unsupported_coverage_payload_format", fmt.Errorf("coverage must be JSON or msgpack"))
			return nil, false
		}
		body, mime, err := coverageBody(raw, checkSuffix(source, ".msgpack"))
		if err != nil {
			result.fail("request_preparation_failed", err)
			return nil, false
		}
		evp := ""
		if !work.urls.agentless {
			evp = "citestcov-intake"
		}
		headers := requestHeaders(work.plan, nil, work.apiKey, evp)
		headers.Set("Content-Type", mime)
		requests = []outbound{{body: body, url: work.urls.coverage, headers: headers}}
	} else {
		payload, err := decodeObject(raw)
		if err != nil {
			result.fail("invalid_telemetry_payload", err)
			return nil, false
		}
		requests, err = telemetryBody(raw, payload, work.plan, source, work.urls, work.apiKey)
		if err != nil {
			result.fail("invalid_telemetry_metadata", err)
			return nil, false
		}
	}
	return requests, true
}

// sendOther delivers a coverage or telemetry source's requests. After a partial
// delivery, the source keeps only the request that failed.
func (work *worker) sendOther(ctx context.Context, index int, requests []outbound) {
	result := &work.results[index]
	var rejected telemetryReplay
	for _, request := range requests {
		delivery := work.send.post(ctx, request, work.compressor)
		result.record(delivery)
		if delivery.Err != nil {
			work.failDelivery(result, delivery)
			rejected = telemetryReplay{Body: request.body, SessionID: request.headers.Get("DD-Session-ID")}
		}
	}
	if result.Status != statusFailed {
		work.finish(index)
	} else if result.Requests.Succeeded > 0 && !work.options.KeepPayloads {
		replay, err := encodeJSON(map[string]telemetryReplay{telemetryReplayKey: rejected})
		if err != nil || work.replace(index, replay) != nil {
			result.warn("failed_telemetry_request_persist_failed")
		}
	}
}

func (work *worker) failDelivery(result *FileResult, delivery delivery) {
	if result.Status != statusFailed {
		code := "http_request_failed"
		if delivery.Status > 0 {
			code = fmt.Sprintf("http_%d", delivery.Status)
		}
		result.fail(code, delivery.Err)
	}
}

func (work *worker) finish(index int) {
	result := &work.results[index]
	result.Status = statusSucceeded
	if work.options.DryRun || work.options.KeepPayloads {
		return
	}
	if !work.unchanged(index) {
		result.warn("source_changed_before_cleanup")
		return
	}
	dir, name, err := work.directory(index)
	if err == nil {
		err = writable(dir, name, func() error { return dir.Remove(name) })
	}
	if err != nil {
		result.warn("source_cleanup_failed")
		return
	}
	result.Deleted = true
}

// writable runs an operation that changes a source in dir. If it fails, it
// makes the source and dir writable once and runs it again: Bazel can leave
// its output trees read-only, and the rules uploader repairs them so too.
func writable(dir *os.Root, name string, operation func() error) error {
	if err := operation(); err == nil || errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, target := range []string{name, "."} {
		if info, err := dir.Lstat(target); err == nil {
			_ = dir.Chmod(target, info.Mode().Perm()|0o200)
		}
	}
	return operation()
}

func (work *worker) unchanged(index int) bool {
	dir, name, err := work.directory(index)
	if err != nil {
		return false
	}
	info, err := dir.Lstat(name)
	original := work.files[index]
	return err == nil && original != nil && os.SameFile(info, original) &&
		info.Size() == original.Size() && info.ModTime().Equal(original.ModTime())
}

func (work *worker) replace(index int, data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("missing replay payload")
	}
	dir, target, err := work.directory(index)
	if err != nil {
		return err
	}
	return writable(dir, target, func() error { return work.writeReplay(index, dir, target, data) })
}

// writeReplay writes data beside the source and renames it over the source.
func (work *worker) writeReplay(index int, dir *os.Root, target string, data []byte) error {
	file := ".ddtest-replay-" + rand.Text()
	out, err := dir.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Remove(file) }()
	_, writeErr := out.Write(data)
	closeErr := out.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	if !work.unchanged(index) {
		return fmt.Errorf("source changed before replay")
	}
	return dir.Rename(file, target)
}

func emptyTestSession(events []any) bool {
	for _, value := range events {
		event := object(value)
		if stringValue(event, "type") != "test_session_end" {
			continue
		}
		content := object(event["content"])
		meta := object(content["meta"])
		metrics := object(content["metrics"])
		session, _ := content["test_session_id"].(json.Number)
		id, err := strconv.ParseUint(session.String(), 10, 64)
		if meta["test.session.empty_reason"] == "zero_tests" && meta["test.status"] == "skip" &&
			err == nil && id > 0 && metrics["test.exit_code"] == json.Number("0") && content["error"] == json.Number("0") {
			return true
		}
	}
	return false
}
