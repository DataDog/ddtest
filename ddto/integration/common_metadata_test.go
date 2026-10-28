package integration

import (
	"fmt"
	"maps"
	"reflect"
	"testing"
)

// Resolve the newly shared CI defaults before the existing semantic comparison.
// Other envelope fields keep their separate ciWireMetadata contract. Never alter
// raw captured payloads: tests also inspect where values actually travelled.
func ciMetadataEvents(payload map[string]any) ([]map[string]any, error) {
	rows, ok := payload["events"].([]any)
	if !ok {
		return nil, fmt.Errorf("missing events")
	}
	metadata, _ := payload["metadata"].(map[string]any)
	events := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		raw, ok := row.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid event")
		}
		content, ok := raw["content"].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("missing event content")
		}
		local, _ := content["meta"].(map[string]any)
		metrics, _ := content["metrics"].(map[string]any)
		meta := make(map[string]any, len(local))
		kind, _ := raw["type"].(string)
		defaults, _ := metadata[kind].(map[string]any)
		for key, value := range defaults {
			if !isSharedCIKind(kind) || !isLiftedCIKey(key) {
				continue
			}
			if _, numeric := metrics[key]; numeric {
				if _, text := local[key]; !text {
					return nil, fmt.Errorf("numeric %s inherits string metadata", key)
				}
			}
			meta[key] = value
		}
		maps.Copy(meta, local)
		event := maps.Clone(raw)
		copy := maps.Clone(content)
		copy["meta"] = meta
		event["content"] = copy
		events = append(events, event)
	}
	return events, nil
}

func TestCIComparatorSharedMetadataRetainsValuesAndOverrides(t *testing.T) {
	payload := func(defaults, meta, metrics map[string]any) map[string]any {
		return map[string]any{"metadata": map[string]any{"test": defaults}, "events": []any{map[string]any{"type": "test", "version": uint64(2), "content": map[string]any{"meta": meta, "metrics": metrics}}}}
	}
	base := payload(nil, map[string]any{"ci.job.name": "job"}, nil)
	want, err := ciMetadataEvents(base)
	if err != nil {
		t.Fatal(err)
	}
	shared := payload(map[string]any{"ci.job.name": "job"}, nil, nil)
	got, err := ciMetadataEvents(shared)
	if err != nil || !reflect.DeepEqual(ciWireEvents(want), ciWireEvents(got)) {
		t.Fatalf("shared defaults changed comparison: %v", err)
	}
	for _, other := range []map[string]any{
		payload(map[string]any{"ci.job.name": "wrong"}, nil, nil),
		payload(nil, nil, nil),
		payload(map[string]any{"ci.job.name": "job"}, map[string]any{"ci.job.name": "override"}, nil),
	} {
		got, err = ciMetadataEvents(other)
		if err != nil || reflect.DeepEqual(ciWireEvents(want), ciWireEvents(got)) {
			t.Fatal("missing, wrong or overridden CI data was masked")
		}
	}
	if _, err := ciMetadataEvents(payload(map[string]any{"ci.job.name": "job"}, nil, map[string]any{"ci.job.name": 42})); err == nil {
		t.Fatal("numeric default collision accepted")
	}
	if !reflect.DeepEqual(shared, payload(map[string]any{"ci.job.name": "job"}, nil, nil)) {
		t.Fatal("semantic comparison changed raw payload")
	}
}

func assertSessionCommonMetadataPlacement(t *testing.T, capture *miniWireCapture) {
	t.Helper()
	found := false
	for _, payload := range capture.payloads {
		metadata := payload["metadata"].(map[string]any)
		for _, row := range payload["events"].([]any) {
			event := row.(map[string]any)
			if event["type"] != "test_session_end" {
				continue
			}
			found = true
			defaults, _ := metadata["test_session_end"].(map[string]any)
			meta := event["content"].(map[string]any)["meta"].(map[string]any)
			for _, key := range []string{"os.platform", "runtime.version", "git.commit.sha"} {
				if value, ok := defaults[key].(string); !ok || value == "" {
					t.Fatalf("session common metadata missing %s", key)
				}
				if _, repeated := meta[key]; repeated {
					t.Fatalf("session repeats shared metadata %s", key)
				}
			}
		}
	}
	if !found {
		t.Fatal("common metadata placement had no session")
	}
}
