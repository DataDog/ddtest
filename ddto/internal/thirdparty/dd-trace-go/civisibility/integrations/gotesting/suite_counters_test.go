// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations"
)

type counterStubModule struct {
	integrations.TestModule
	name   string
	closed int
}

func (m *counterStubModule) Name() string { return m.name }

func (m *counterStubModule) Close(...integrations.TestModuleCloseOption) { m.closed++ }

type counterStubSuite struct {
	integrations.TestSuite
	name   string
	closed int
}

func (s *counterStubSuite) Name() string { return s.name }

func (s *counterStubSuite) Close(...integrations.TestSuiteCloseOption) { s.closed++ }

// Suite names are file names, so two modules can each have a suite of the
// same name. Each suite closes after its own tests, not after the other's.
func TestSuiteCountersAreKeyedByModule(t *testing.T) {
	first := &counterStubModule{name: "example.com/counters/first"}
	second := &counterStubModule{name: "example.com/counters/second"}
	firstSuite := &counterStubSuite{name: "helpers.go"}
	secondSuite := &counterStubSuite{name: "helpers.go"}
	for _, module := range []*counterStubModule{first, second} {
		addModulesCounters(module.name, 1)
		addSuitesCounters(module.name, "helpers.go", 1)
	}
	addModulesCounters(second.name, 1)
	addSuitesCounters(second.name, "helpers.go", 1)
	t.Cleanup(func() {
		for _, module := range []*counterStubModule{first, second} {
			addModulesCounters(module.name, -addModulesCounters(module.name, 0))
			addSuitesCounters(module.name, "helpers.go", -addSuitesCounters(module.name, "helpers.go", 0))
		}
	})

	checkModuleAndSuite(first, firstSuite)
	if firstSuite.closed != 1 || first.closed != 1 {
		t.Fatalf("first suite closed %d times, module %d times", firstSuite.closed, first.closed)
	}
	if secondSuite.closed != 0 || second.closed != 0 {
		t.Fatal("the second module's suite closed with the first")
	}
	checkModuleAndSuite(second, secondSuite)
	if secondSuite.closed != 0 {
		t.Fatal("the second suite closed before its last test")
	}
	checkModuleAndSuite(second, secondSuite)
	if secondSuite.closed != 1 || second.closed != 1 {
		t.Fatalf("second suite closed %d times, module %d times", secondSuite.closed, second.closed)
	}
}
