package minitracer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/citransport"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/msgp/msgp"
)

// For any mix of kinds, snapshots and overrides, the accounted size bounds the
// payload that projection and encoding produce, and the shared total applies
// only while the batch keeps one snapshot per test-event kind. A sealed batch
// leaves nothing behind: the next one accounts as if it were the first.
// Projection searches only the events that eventSize found overriding, and
// produces what searching every event produces.
func TestBatchBytesBoundProjectedPayload(t *testing.T) {
	base := map[string]map[string]string{"*": {"language": "go"}, "test": {"test_session.name": "session", "ci.job.name": "older"}}
	envelope := (&testCyclePayload{Version: 1, Metadata: base}).Msgsize() + msgp.ArrayHeaderSize
	message := strings.Repeat("m", 600)
	one := NewCommonTags(map[string]string{"ci.job.name": "job", "git.commit.sha": "one", "git.commit.message": message})
	two := NewCommonTags(map[string]string{"ci.job.name": "job", "git.commit.sha": "two", "git.commit.message": message})
	snapshots := []*CommonTags{one, one, one, one, two, nil}
	kinds := []string{"test", "test", "test", "test_suite_end", "span"}
	random := rand.New(rand.NewPCG(23, 23))
	var shared, unshared int
	for run := range 300 {
		var accounted batchBytes
		var events ciEvents
		for i := range 12 {
			if i > 0 && random.IntN(6) == 0 {
				accounted.reset()
				events = nil
			}
			event := &ciEvent{Type: kinds[random.IntN(len(kinds))], Version: 1, common: snapshots[random.IntN(len(snapshots))],
				Content: eventContent{Name: "test", Meta: map[string]string{"test.name": fmt.Sprint(i)}}}
			switch random.IntN(25) {
			case 0:
				event.Content.Meta["git.commit.message"] = "override"
			case 1:
				event.Content.Metrics = map[string]float64{"git.commit.message": 1}
			}
			size, overrides := eventSize(event)
			event.overrides = overrides // As Client.add records it.
			want := accounted.with(event, size, overrides)
			accounted.add(event, size, overrides)
			if accounted.total() != want {
				t.Fatalf("run %d: with=%d, add then total=%d", run, want, accounted.total())
			}
			events = append(events, event)
			var fresh batchBytes
			for _, event := range events {
				size, overrides := eventSize(event)
				fresh.add(event, size, overrides)
			}
			if fresh.total() != accounted.total() || fresh.worst != accounted.worst || fresh.unshared != accounted.unshared {
				t.Fatalf("run %d: after a reset, %+v differs from %+v", run, accounted, fresh)
			}
			metadata, wire := prepareCommonMetadata(base, events)
			searched := make(ciEvents, len(events))
			for i, event := range events {
				every := *event
				every.overrides = true
				searched[i] = &every
			}
			everyMetadata, everyWire := prepareCommonMetadata(base, searched)
			if !reflect.DeepEqual(metadata, everyMetadata) {
				t.Fatalf("run %d: metadata %v, searching every event %v", run, metadata, everyMetadata)
			}
			for i := range wire {
				if wire[i].Type != everyWire[i].Type || !reflect.DeepEqual(wire[i].Content, everyWire[i].Content) {
					t.Fatalf("run %d: event %d is %+v, searching every event %+v", run, i, wire[i].Content, everyWire[i].Content)
				}
			}
			var raw bytes.Buffer
			if err := msgp.Encode(&raw, &testCycleBatch{Version: 1, Metadata: metadata, Events: wire}); err != nil {
				t.Fatal(err)
			}
			if raw.Len() > accounted.total()+envelope {
				t.Fatalf("run %d: %d events encode to %d bytes, accounted %d", run, len(events), raw.Len(), accounted.total()+envelope)
			}
			if accounted.unshared {
				unshared++
			} else {
				shared++
				if accounted.total() > accounted.worst {
					t.Fatalf("run %d: shared %d above worst %d", run, accounted.total(), accounted.worst)
				}
			}
		}
	}
	if shared < 300 || unshared < 300 {
		t.Fatalf("mixes did not exercise both totals: shared=%d unshared=%d", shared, unshared)
	}
}

// While sharing applies, the worst case of a large snapshot can grow beyond
// what a 32-bit int holds. It stops at maxWorstBytes, which still exceeds the
// flush threshold, so an event that ends sharing seals the batch first.
func TestBatchBytesWorstCaseSaturates(t *testing.T) {
	common := NewCommonTags(map[string]string{"ci.large": strings.Repeat("c", (2<<20)+(128<<10))})
	var accounted batchBytes
	for i := range 1000 {
		event := &ciEvent{Type: "test", Version: 1, common: common, Content: eventContent{Name: "test", Meta: map[string]string{"test.name": fmt.Sprint(i)}}}
		size, overrides := eventSize(event)
		accounted.add(event, size, overrides)
	}
	if accounted.unshared || accounted.total() >= citransport.TestCycleFlushBytes || accounted.worst != maxWorstBytes {
		t.Fatalf("shared=%d worst=%d unshared=%t", accounted.shared, accounted.worst, accounted.unshared)
	}
	breaking := &ciEvent{Type: "test", Version: 1, Content: eventContent{Name: "test"}}
	size, overrides := eventSize(breaking)
	if with := accounted.with(breaking, size, overrides); with <= citransport.TestCycleMaxPayloadBytes {
		t.Fatalf("an event ending sharing accounts for %d bytes", with)
	}
}

// A homogeneous batch writes its snapshot once, so a large snapshot no longer
// seals batches below the event cap. An event that would break that sharing
// seals the batch first: projected peers would otherwise carry the snapshot,
// here about 6.4 MB against the 5 MiB intake limit.
func TestSharedSnapshotBatches(t *testing.T) {
	large := map[string]string{"ci.large": strings.Repeat("c", 32<<10), "git.commit.sha": "commit"}
	common := NewCommonTags(large)
	test := func(i int, common *CommonTags) *ciEvent {
		return &ciEvent{Type: "test", Version: 1, common: common, Content: eventContent{Name: "test", Meta: map[string]string{"test.name": fmt.Sprint(i)}}}
	}
	override := func(event *ciEvent) *ciEvent {
		event.Content.Meta["ci.large"] = "override"
		return event
	}
	// requests is the number of payloads; one of them holds contains events.
	for _, tc := range []struct {
		name               string
		event              func(i int) *ciEvent
		total              int
		requests, contains int
	}{
		{"homogeneous", func(i int) *ciEvent { return test(i, common) }, 2000, 2, 1000},
		{"override", func(i int) *ciEvent {
			if i == 200 {
				return override(test(i, common))
			}
			return test(i, common)
		}, 300, 3, 200},
		{"metric override", func(i int) *ciEvent {
			event := test(i, common)
			if i == 200 {
				event.Content.Metrics = map[string]float64{"ci.large": 1}
			}
			return event
		}, 300, 3, 200},
		{"other snapshot", func(i int) *ciEvent {
			if i == 200 {
				return test(i, NewCommonTags(large))
			}
			return test(i, common)
		}, 300, 3, 200},
		{"no snapshot", func(i int) *ciEvent {
			if i == 200 {
				return test(i, nil)
			}
			return test(i, common)
		}, 300, 3, 200},
		// Other kinds carry their snapshot, and their overrides end no sharing.
		{"other kind", func(i int) *ciEvent {
			event := test(i, common)
			event.Type = "span"
			return event
		}, 200, 3, 0},
		{"other kind override", func(i int) *ciEvent {
			if i%500 == 0 {
				event := override(test(i, NewCommonTags(large)))
				event.Type = "span"
				return event
			}
			return test(i, common)
		}, 2000, 2, 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var counts []int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil || len(body) > citransport.TestCycleFlushBytes {
					t.Errorf("body=%d error=%v", len(body), err)
				}
				var envelope testCyclePayload
				var events ciEvents
				if err := msgp.Decode(bytes.NewReader(body), &envelope); err != nil {
					t.Error(err)
				}
				if err := msgp.Decode(bytes.NewReader(envelope.Events), &events); err != nil {
					t.Error(err)
				}
				mu.Lock()
				counts = append(counts, len(events))
				mu.Unlock()
				w.WriteHeader(202)
			}))
			defer server.Close()
			client, err := New(Config{Transport: citransport.Config{Endpoint: server.URL}})
			if err != nil {
				t.Fatal(err)
			}
			for i := range tc.total {
				client.add(tc.event(i))
			}
			if err := client.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			slices.Sort(counts)
			sum := 0
			for _, n := range counts {
				sum += n
			}
			if sum != tc.total || client.DroppedEvents() != 0 || len(counts) != tc.requests || tc.contains != 0 && !slices.Contains(counts, tc.contains) {
				t.Fatalf("events per request %v, dropped %d", counts, client.DroppedEvents())
			}
			// Encoding reserves the accounted size, so a buffer for a small
			// payload stays small enough to be kept for the next one.
			client.mu.Lock()
			kept := client.payload.Cap() > 0 || slices.ContainsFunc(client.buffers, func(b *bytes.Buffer) bool { return b.Cap() > 0 })
			client.mu.Unlock()
			if tc.name == "homogeneous" && !kept {
				t.Fatal("encoding buffers were released")
			}
		})
	}
}
