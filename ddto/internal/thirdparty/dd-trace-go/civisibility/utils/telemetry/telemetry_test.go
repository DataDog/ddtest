// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package telemetry

import (
	"context"
	"math"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	globaltelemetry "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/telemetry/telemetrytest"
)

func TestRemoveEmptyStrings(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "All non-empty strings",
			input: []string{"hello", "world"},
			want:  []string{"hello", "world"},
		},
		{
			name:  "All empty strings",
			input: []string{"", "", ""},
			want:  []string{},
		},
		{
			name:  "Mixed empty and non-empty strings",
			input: []string{"one", "", "two", "", "three"},
			want:  []string{"one", "two", "three"},
		},
		{
			name:  "Empty slice",
			input: []string{},
			want:  []string{},
		},
		{
			name:  "Empty string at the beginning",
			input: []string{"", "start", "end"},
			want:  []string{"start", "end"},
		},
		{
			name:  "Empty string at the end",
			input: []string{"start", "end", ""},
			want:  []string{"start", "end"},
		},
		{
			name:  "Multiple consecutive empty strings",
			input: []string{"start", "", "", "end", ""},
			want:  []string{"start", "end"},
		},
		{
			name:  "Strings with spaces (not considered empty)",
			input: []string{" ", "text", "", "  "},
			want:  []string{" ", "text", "  "},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := removeEmptyStrings(tc.input)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("removeEmptyStrings(%v) = %v; expected %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestGetProviderTestSessionTypeFromProviderString(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		want     TestSessionType
	}{
		{
			name:     "Bazel provider",
			provider: "bazel",
			want:     BazelTestSessionType,
		},
		{
			name:     "Existing provider",
			provider: "github",
			want:     GithubActionsTestSessionType,
		},
		{
			name:     "Unknown provider",
			provider: "something-else",
			want:     UnsupportedTestSessionType,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := getProviderTestSessionTypeFromProviderString(tc.provider)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("getProviderTestSessionTypeFromProviderString(%q) = %v; expected %v", tc.provider, got, tc.want)
			}
		})
	}
}

func TestEventCountersKeepCanonicalAndFeatureTagsAcrossClients(t *testing.T) {
	for _, tc := range []struct {
		name, framework, tags string
		eventType             TestingEventType
	}{
		{"test", "golang.org/pkg/testing", "event_type:test,test_framework:testing", TestingEventType{"event_type:test"}},
		{"suite", "golang.org/pkg/testing", "event_type:suite,test_framework:testing", TestingEventType{"event_type:suite"}},
		{"module", "golang.org/pkg/testing", "event_type:module,test_framework:testing", TestingEventType{"event_type:module"}},
		{"session", "golang.org/pkg/testing", "event_type:session,test_framework:testing", TestingEventType{"event_type:session"}},
		{"unknown framework", "custom", "event_type:test,test_framework:unknown", TestingEventType{"event_type:test"}},
		{"retry", "golang.org/pkg/testing", "event_type:test,is_retry:true,test_framework:testing", TestingEventType{"event_type:test", "is_retry:true"}},
		{"efd quarantine", "golang.org/pkg/testing", "event_type:test,is_new:true,is_quarantined:true,test_framework:testing", TestingEventType{"event_type:test", "is_new:true", "is_quarantined:true"}},
		{"empty tag", "golang.org/pkg/testing", "event_type:test,test_framework:testing", TestingEventType{"", "event_type:test"}},
		{"custom event", "golang.org/pkg/testing", "event_type:custom,test_framework:testing", TestingEventType{"event_type:custom"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Repeat with new registries: retained handles must not target an
			// earlier test client after MockClient clears the global cache.
			for i := 0; i < 2; i++ {
				client := &telemetrytest.RecordClient{}
				restore := globaltelemetry.MockClient(client)
				EventCreated(tc.framework, tc.eventType)
				EventFinished(tc.framework, tc.eventType)
				EventsEnqueueForSerialization()
				for _, name := range []string{"event_created", "event_finished", "events_enqueued_for_serialization"} {
					tags := tc.tags
					if name == "events_enqueued_for_serialization" {
						tags = ""
					}
					key := telemetrytest.MetricKey{Namespace: globaltelemetry.NamespaceCIVisibility, Kind: "count", Name: name, Tags: tags}
					if metric := client.Metrics[key]; metric == nil || metric.Get() != 1 {
						t.Errorf("lost/changed metric %v: %v", key, metric)
					}
				}
				if len(client.Metrics) != 3 {
					t.Errorf("unexpected metrics: %v", client.Metrics)
				}
				restore()
			}
		})
	}
}

func TestEventCountersDisabled(t *testing.T) {
	if os.Getenv("DDTO_DISABLED_COUNTER_HELPER") == "1" {
		client := &telemetrytest.RecordClient{}
		defer globaltelemetry.MockClient(client)()
		EventCreated("golang.org/pkg/testing", TestEventType)
		EventFinished("golang.org/pkg/testing", TestingEventType{"event_type:test", "is_retry:true"})
		EventsEnqueueForSerialization()
		handle := globaltelemetry.BindCount(globaltelemetry.NamespaceCIVisibility, "event_created", []string{"event_type:test"})
		handle.Submit(1)
		if !math.IsNaN(handle.Get()) || len(client.Metrics) != 0 {
			t.Fatal("disabled telemetry recorded a bound or ordinary metric")
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestEventCountersDisabled$")
	cmd.Env = append(os.Environ(), "DDTO_DISABLED_COUNTER_HELPER=1", "DD_INSTRUMENTATION_TELEMETRY_ENABLED=false")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("disabled counter check: %v\n%s", err, out)
	}
}
