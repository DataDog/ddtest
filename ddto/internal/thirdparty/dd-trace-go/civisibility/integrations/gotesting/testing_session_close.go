// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"flag"
	"strings"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

// testingMSessionCloseOptions returns the options for closing the session
// once testing.M.Run has finished: an empty successful session is skipped
// only when every workload was wrapped to create a module.
func testingMSessionCloseOptions(claim *testingMInstrumentationClaim) []integrations.TestSessionCloseOption {
	if claim.instrumentedAllWorkloads() {
		return []integrations.TestSessionCloseOption{integrations.WithTestSessionSkipIfEmpty()}
	}
	if log.DebugEnabled() {
		log.Debug("civisibility: a session that runs nothing will not be reported as skipped: wrapped_tests=%t wrapped_fuzz=%t wrapped_examples=%t wrapped_benchmarks=%t",
			claim.testDescriptors != nil, claim.fuzzDescriptors != nil, claim.exampleDescriptors != nil, claim.benchmarkDescriptors != nil)
	}
	return nil
}

// instrumentedAllWorkloads reports whether every workload that testing.M.Run
// could execute was wrapped to create a module, so a session that closes
// without modules ran nothing. Benchmarks are wrapped only when os.Args selects
// them, but TestMain or a flag callback can set test.bench too. Unwrapped
// benchmarks count as possibly run if the flag has a value or was ever set,
// since a later flag.Set may have cleared it.
func (c *testingMInstrumentationClaim) instrumentedAllWorkloads() bool {
	if c.testDescriptors == nil || c.fuzzDescriptors == nil || c.exampleDescriptors == nil {
		return false
	}
	if c.benchmarkDescriptors != nil {
		return true
	}
	if bench := flag.Lookup("test.bench"); bench == nil || bench.Value.String() != "" {
		return false
	}
	benchSet := false
	flag.Visit(func(f *flag.Flag) { benchSet = benchSet || f.Name == "test.bench" })
	return !benchSet
}

// subBenchmarkPatterns reports whether -bench or -skip select sub-benchmarks
// by name: a "/" separates the levels that testing matches.
func subBenchmarkPatterns() bool {
	for _, name := range []string{"test.bench", "test.skip"} {
		if f := flag.Lookup(name); f != nil && strings.Contains(f.Value.String(), "/") {
			return true
		}
	}
	return false
}
