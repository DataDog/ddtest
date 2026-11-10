// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package integrations

// declarationUnskippableFixtureFunc provides a stable runtime symbol for declaration-level unskippable tests.
//
//dd:test.unskippable
func declarationUnskippableFixtureFunc() {}

// declarationExclusionsFixtureFunc requests both test automation exclusions.
//
//dd:test.no_auto_retry
//dd:test.prevent_failure_suppression
func declarationExclusionsFixtureFunc() {}

type declarationExclusionsFixtureSuite struct{}

// TestOnce is a method that requests no automatic retries only.
//
//dd:test.no_auto_retry
func (*declarationExclusionsFixtureSuite) TestOnce() {}
