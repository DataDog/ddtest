// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package integrations

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	tracer "github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/telemetry"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/locking"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/log"
)

// Test Session

// Ensures that tslvTestSession implements the TestSession interface.
var _ TestSession = (*tslvTestSession)(nil)

// tslvTestSession implements the DdTestSession interface and represents a session for a set of tests.
type tslvTestSession struct {
	ciVisibilityCommon
	sessionID        uint64
	sessionIDText    string // sessionID formatted once for test events
	command          string
	workingDirectory string
	framework        string
	frameworkVersion string
	efdAbortReasonMu locking.Mutex
	efdAbortReason   string

	modules map[string]TestModule

	// cliRecord is set when this process reports in the session that ddto
	// owns: the process then sends no session event, and records the tags it
	// sets, in cliTags, for ddto's.
	cliRecord *utils.CLISessionRecord
	cliTagsMu sync.Mutex
	cliTags   map[string]any
}

// CreateTestSession initializes a new test session with the given command and working directory.
func CreateTestSession(options ...TestSessionStartOption) TestSession {
	if IsProcessRetryChild() {
		return newProcessRetryNoopSession(options...)
	}

	defaults := &tslvTestSessionStartOptions{}
	for _, f := range options {
		f(defaults)
	}
	handed, record := utils.HandedCLISession()
	if handed != nil {
		// The command is already ddto's, in the CI tags.
		defaults.id = handed.ID
		defaults.workingDirectory = utils.GetRelativePathFromCITagsSourceRoot(handed.WorkingDirectory)
	}

	if defaults.command == "" {
		defaults.command = utils.GetCITagsReadOnly()[constants.TestCommand]
	}
	if defaults.workingDirectory == "" {
		wd, err := os.Getwd()
		if err == nil {
			wd = utils.GetRelativePathFromCITagsSourceRoot(wd)
		}
		defaults.workingDirectory = wd
	}
	if defaults.startTime.IsZero() {
		defaults.startTime = time.Now()
	}

	// Ensure CI visibility is properly configured.
	EnsureCiVisibilityInitialization()

	sessionTags := []tracer.StartSpanOption{
		ciVisibilityTag(constants.TestType, constants.TestTypeTest),
		ciVisibilityTag(constants.TestCommand, defaults.command),
		ciVisibilityTag(constants.TestCommandWorkingDirectory, defaults.workingDirectory),
	}

	operationName := "test_session"
	if defaults.framework != "" {
		operationName = fmt.Sprintf("%s.%s", strings.ToLower(defaults.framework), operationName)
		sessionTags = append(sessionTags,
			ciVisibilityTag(constants.TestFramework, defaults.framework),
			ciVisibilityTag(constants.TestFrameworkVersion, defaults.frameworkVersion))
	}

	resourceName := fmt.Sprintf("%s.%s", operationName, defaults.command)

	testOpts := append(fillCommonTags([]tracer.StartSpanOption{
		tracer.ResourceName(resourceName),
		tracer.SpanType(constants.SpanTypeTestSession),
		tracer.StartTime(defaults.startTime),
	}), sessionTags...)

	span, ctx := tracer.StartSpanFromContext(context.Background(), operationName, testOpts...)
	sessionID := span.Context().SpanID()
	if defaults.id != 0 {
		sessionID = defaults.id
	}
	setCIVisibilitySpanTag(span, constants.TestSessionIDTag, strconv.FormatUint(sessionID, 10))

	s := &tslvTestSession{
		sessionID:        sessionID,
		sessionIDText:    strconv.FormatUint(sessionID, 10),
		command:          defaults.command,
		workingDirectory: defaults.workingDirectory,
		framework:        defaults.framework,
		frameworkVersion: defaults.frameworkVersion,
		modules:          map[string]TestModule{},
		ciVisibilityCommon: ciVisibilityCommon{
			startTime: defaults.startTime,
			tags:      sessionTags,
			span:      span,
			ctx:       ctx,
		},
	}

	// Ensure to close everything before CI visibility exits. In CI visibility mode, we try to never lose data.
	PushCiVisibilityCloseAction(func() { s.Close(1) })
	if record != nil {
		// Each test process keeps reporting its session's telemetry, as
		// without ddto.
		s.cliRecord, s.cliTags = record, map[string]any{}
		log.Debug("civisibility: reporting in ddto's test session id=%d", sessionID)
	}

	// Creating telemetry event created
	testingEventType := telemetry.SessionEventType
	if utils.GetCodeOwners() != nil {
		testingEventType = append(testingEventType, telemetry.HasCodeOwnerEventType...)
	}

	ciProviderName, hasCiProvider := utils.GetCITagsReadOnly()[constants.CIProviderName]
	if !hasCiProvider {
		testingEventType = append(testingEventType, telemetry.UnsupportedCiEventType...)
	}

	// Write test session telemetry
	telemetry.TestSession(ciProviderName)
	telemetry.EventCreated(s.framework, testingEventType)
	return s
}

// SessionID returns the ID of the test session.
func (t *tslvTestSession) SessionID() uint64 {
	return t.sessionID
}

// SetTag sets a session tag and retains the EFD abort reason needed when
// producing the session-finished telemetry event.
func (t *tslvTestSession) SetTag(key string, value any) {
	t.ciVisibilityCommon.SetTag(key, value)
	if t.cliRecord != nil {
		t.cliTagsMu.Lock()
		t.cliTags[key] = value
		t.cliTagsMu.Unlock()
	}
	if key == constants.TestEarlyFlakeDetectionRetryAborted {
		t.efdAbortReasonMu.Lock()
		t.efdAbortReason = fmt.Sprint(value)
		t.efdAbortReasonMu.Unlock()
	}
}

// Command returns the command used to run the test session.
func (t *tslvTestSession) Command() string { return t.command }

// Framework returns the testing framework used in the test session.
func (t *tslvTestSession) Framework() string { return t.framework }

// WorkingDirectory returns the working directory of the test session.
func (t *tslvTestSession) WorkingDirectory() string { return t.workingDirectory }

// Close closes the test session with the given exit code.
func (t *tslvTestSession) Close(exitCode int, options ...TestSessionCloseOption) {
	alreadyClosed := false
	recordedTags := -1
	// After unlocking: a client can replace the logger.
	defer func() {
		if recordedTags >= 0 {
			log.Debug("civisibility: recorded the close of this process in ddto's test session exit_code=%d tags=%d", exitCode, recordedTags)
		}
	}()
	if log.DebugEnabled() {
		started := time.Now()
		log.Debug("civisibility: session close started exit_code=%d", exitCode)
		// Includes the session lock, module closure and tracer.Flush. Emit after
		// unlocking; a delivery failure is reported by the tracer, not exit_code.
		defer func() {
			log.Debug("civisibility: session close finished duration=%s exit_code=%d already_closed=%t", time.Since(started), exitCode, alreadyClosed)
		}()
	}
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.closed {
		alreadyClosed = true
		return
	}

	defaults := &tslvTestSessionCloseOptions{}
	for _, f := range options {
		f(defaults)
	}

	if defaults.finishTime.IsZero() {
		defaults.finishTime = time.Now()
	}

	for _, m := range t.modules {
		m.Close()
	}
	t.modules = map[string]TestModule{}

	if t.cliRecord != nil {
		recordedTags = t.closeInCLISession(exitCode)
		t.reportFinished()
		tracer.Flush()
		return
	}

	setCIVisibilitySpanTag(t.span, constants.TestCommandExitCode, exitCode)
	if exitCode == 0 {
		setCIVisibilitySpanTag(t.span, constants.TestStatus, constants.TestStatusPass)
	} else if defaults.crash != nil {
		t.SetError(defaults.crash...)
		setCIVisibilitySpanTag(t.span, constants.TestStatus, constants.TestStatusFail)
	} else {
		t.SetError(WithErrorInfo("ExitCode", "exit code is not zero.", ""))
		setCIVisibilitySpanTag(t.span, constants.TestStatus, constants.TestStatusFail)
	}
	// Native startup can finish before asynchronous CI feature discovery.
	// Publish newly discovered capabilities and ITR correlation before sealing
	// the session event, preserving explicit session attributes.
	for key, value := range utils.GetCITagsReadOnly() {
		if strings.HasPrefix(key, "_dd.library_capabilities.") || key == constants.ItrCorrelationIDTag {
			if _, exists := t.GetTag(key); !exists {
				t.SetTag(key, value)
			}
		}
	}
	t.span.Finish(tracer.FinishTime(defaults.finishTime))
	t.closed = true
	t.reportFinished()
	tracer.Flush()
}

// reportFinished reports the session's finished-event telemetry.
func (t *tslvTestSession) reportFinished() {
	t.efdAbortReasonMu.Lock()
	faultyEFDSession := t.efdAbortReason == "faulty"
	t.efdAbortReasonMu.Unlock()
	testingEventType := telemetry.SessionEventType
	if utils.GetCodeOwners() != nil {
		testingEventType = append(testingEventType, telemetry.HasCodeOwnerEventType...)
	}
	if _, hasCiProvider := utils.GetCITagsReadOnly()[constants.CIProviderName]; !hasCiProvider {
		testingEventType = append(testingEventType, telemetry.UnsupportedCiEventType...)
	}
	if faultyEFDSession {
		testingEventType = append(testingEventType, telemetry.EfdAbortFaultyEventType...)
	}
	telemetry.EventFinished(t.framework, testingEventType)
}

// GetOrCreateModule returns an existing module or creates a new one with the given name, framework, framework version, and start time.
func (t *tslvTestSession) GetOrCreateModule(name string, options ...TestModuleStartOption) TestModule {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	defaults := &tslvTestModuleStartOptions{}
	for _, f := range options {
		f(defaults)
	}

	if defaults.framework == "" {
		defaults.framework = t.framework
		defaults.frameworkVersion = t.frameworkVersion
	}
	if defaults.startTime.IsZero() {
		defaults.startTime = time.Now()
	}

	var mod TestModule
	if v, ok := t.modules[name]; ok {
		mod = v
	} else {
		mod = createTestModule(t, name, defaults)
		t.modules[name] = mod
	}

	return mod
}

// closeInCLISession closes a session that ddto owns: it records, for ddto's
// session event, the exit code and the session tags that this process set,
// including those that the CI tags carry, as Close does, and returns how many
// tags it recorded. The session event itself is ddto's.
func (t *tslvTestSession) closeInCLISession(exitCode int) int {
	t.cliTagsMu.Lock()
	tags := make(map[string]any, len(t.cliTags))
	for key, value := range t.cliTags {
		tags[key] = value
	}
	t.cliTagsMu.Unlock()
	for key, value := range utils.GetCITagsReadOnly() {
		if strings.HasPrefix(key, "_dd.library_capabilities.") || key == constants.ItrCorrelationIDTag {
			if _, set := tags[key]; !set {
				tags[key] = value
			}
		}
	}
	t.cliRecord.Append(utils.CLISessionRecordLine{Kind: "closed", ExitCode: exitCode, Tags: tags})
	t.closed = true
	return len(tags)
}
