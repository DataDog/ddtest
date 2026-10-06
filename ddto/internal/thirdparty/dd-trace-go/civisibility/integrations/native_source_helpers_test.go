// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integrations

import "time"

// Source metadata tests use native spans; writer behavior has separate wire tests.
func initializeSourceMetadataTestRuntime() {
	disableAdditionalFeaturesForBootstrapTest()
	initializeCIVisibilityLifecycleForTesting()
}

func createDDTestSession(now time.Time) TestSession {
	session := CreateTestSession(WithTestSessionCommand("my-command"), WithTestSessionWorkingDirectory("/tmp/wd"), WithTestSessionFramework("my-testing-framework", "framework-version"), WithTestSessionStartTime(now))
	session.SetTag("my-tag", "my-value")
	return session
}

func createDDTestModule(now time.Time) (TestSession, TestModule) {
	session := createDDTestSession(now)
	module := session.GetOrCreateModule("my-module", WithTestModuleFramework("my-module-framework", "framework-version"), WithTestModuleStartTime(now))
	module.SetTag("my-tag", "my-value")
	return session, module
}

func createDDTestSuite(now time.Time) (TestSession, TestModule, TestSuite) {
	session, module := createDDTestModule(now)
	suite := module.GetOrCreateSuite("my-suite", WithTestSuiteStartTime(now))
	suite.SetTag("my-tag", "my-value")
	return session, module, suite
}

func createDDTest(now time.Time) (TestSession, TestModule, TestSuite, Test) {
	session, module, suite := createDDTestSuite(now)
	test := suite.CreateTest("my-test", WithTestStartTime(now))
	test.SetTag("my-tag", "my-value")
	return session, module, suite, test
}
