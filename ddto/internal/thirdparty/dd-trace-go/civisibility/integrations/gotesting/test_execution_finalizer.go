// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"fmt"
	"testing"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/ddtrace/ext"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

func finalizeInstrumentedTestExecution(
	t *testing.T,
	execMeta *testExecutionMetadata,
	test integrations.Test,
	suite integrations.TestSuite,
	module integrations.TestModule,
	duration time.Duration,
	attemptOutput []byte,
	terminal any,
	terminalStack string,
	markContainersOnTerminal bool,
) {
	if isAnEfdExecution(execMeta) && duration >= bucketBoundFiveM {
		test.SetTag(constants.TestEarlyFlakeDetectionRetryAborted, "slow")
	}
	logTestFinished(t, duration, terminal)

	collectAndWriteLogs(t, test, attemptOutput)
	if terminal != nil {
		t.Fail()
		execMeta.panicData = terminal
		execMeta.panicStacktrace = terminalStack
		finalExec := isFinalExecution(true, false, execMeta, duration)
		if finalExec {
			finalStatus := calculateFinalStatus(execMeta.anyExecutionPassed, true, false, execMeta.isQuarantined, execMeta.isDisabled, execMeta.isAttemptToFix)
			test.SetTag(constants.TestFinalStatus, finalStatusValue(finalStatus))
		}
		if execMeta.isARetry && finalExec {
			if execMeta.allRetriesFailed {
				test.SetTag(constants.TestHasFailedAllRetries, "true")
			}
			if execMeta.isAttemptToFix {
				test.SetTag(constants.TestAttemptToFixPassed, "false")
			}
		}
		test.SetError(integrations.WithErrorInfo("panic", fmt.Sprint(terminal), terminalStack))
		if markContainersOnTerminal {
			suite.SetTag(ext.Error, true)
			module.SetTag(ext.Error, true)
		}
		deferOrCloseInstrumentedTestEvent(execMeta, test, integrations.ResultStatusFail, "")
		return
	}

	failed := t.Failed()
	skipped := t.Skipped()
	finalExec := isFinalExecution(failed, skipped, execMeta, duration)
	switch {
	case failed:
		if finalExec {
			finalStatus := calculateFinalStatus(execMeta.anyExecutionPassed, true, false, execMeta.isQuarantined, execMeta.isDisabled, execMeta.isAttemptToFix)
			test.SetTag(constants.TestFinalStatus, finalStatusValue(finalStatus))
		}
		if execMeta.isARetry && finalExec {
			if execMeta.allRetriesFailed {
				test.SetTag(constants.TestHasFailedAllRetries, "true")
			}
			if execMeta.isAttemptToFix {
				test.SetTag(constants.TestAttemptToFixPassed, "false")
			}
		}
		if execMeta.panicData != nil {
			test.SetError(integrations.WithErrorInfo("panic", fmt.Sprint(execMeta.panicData), execMeta.panicStacktrace))
		} else {
			test.SetTag(ext.Error, true)
		}
		suite.SetTag(ext.Error, true)
		module.SetTag(ext.Error, true)
		deferOrCloseInstrumentedTestEvent(execMeta, test, integrations.ResultStatusFail, "")
	case skipped:
		if finalExec {
			finalStatus := calculateFinalStatus(execMeta.anyExecutionPassed, execMeta.anyExecutionFailed, true, execMeta.isQuarantined, execMeta.isDisabled, execMeta.isAttemptToFix)
			test.SetTag(constants.TestFinalStatus, finalStatusValue(finalStatus))
		}
		if execMeta.isAttemptToFix && execMeta.isARetry && finalExec {
			test.SetTag(constants.TestAttemptToFixPassed, "false")
		}
		if execMeta.skipReason != "" {
			deferOrCloseInstrumentedTestEvent(execMeta, test, integrations.ResultStatusSkip, execMeta.skipReason)
		} else {
			deferOrCloseInstrumentedTestEvent(execMeta, test, integrations.ResultStatusSkip, "")
		}
	default:
		if finalExec {
			finalStatus := calculateFinalStatus(true, execMeta.anyExecutionFailed, false, execMeta.isQuarantined, execMeta.isDisabled, execMeta.isAttemptToFix)
			test.SetTag(constants.TestFinalStatus, finalStatusValue(finalStatus))
		}
		if execMeta.isAttemptToFix && execMeta.isARetry && finalExec {
			if execMeta.allAttemptsPassed {
				test.SetTag(constants.TestAttemptToFixPassed, "true")
			} else {
				test.SetTag(constants.TestAttemptToFixPassed, "false")
			}
		}
		deferOrCloseInstrumentedTestEvent(execMeta, test, integrations.ResultStatusPass, "")
	}
}

// logTestFinished records, in the debug log, how each test ended. With the
// line that each test's start writes, it shows which test ran when a process
// ended, and why a test failed.
func logTestFinished(t *testing.T, duration time.Duration, terminal any) {
	if !log.DebugEnabled() {
		return
	}
	switch {
	case terminal != nil:
		log.Debug("gotesting: test finished name=%s status=fail duration=%s panic=%q", t.Name(), duration, fmt.Sprint(terminal))
	case t.Failed():
		log.Debug("gotesting: test finished name=%s status=fail duration=%s", t.Name(), duration)
	case t.Skipped():
		log.Debug("gotesting: test finished name=%s status=skip duration=%s", t.Name(), duration)
	default:
		log.Debug("gotesting: test finished name=%s status=pass duration=%s", t.Name(), duration)
	}
}

// finalStatusValue returns a final status as an interface. Converting a
// string variable to one allocates; the known statuses are constants, which
// do not.
func finalStatusValue(status string) any {
	switch status {
	case constants.TestStatusPass:
		return constants.TestStatusPass
	case constants.TestStatusFail:
		return constants.TestStatusFail
	case constants.TestStatusSkip:
		return constants.TestStatusSkip
	}
	return status
}
