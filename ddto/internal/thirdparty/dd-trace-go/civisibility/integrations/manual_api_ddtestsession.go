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
	"time"

	tracer "github.com/tonyredondo/dd-ci-testing-poc/internal/minitracer"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/constants"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/telemetry"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/locking"
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

	if defaults.command == "" {
		defaults.command = utils.GetCITags()[constants.TestCommand]
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

	// Creating telemetry event created
	testingEventType := telemetry.SessionEventType
	if utils.GetCodeOwners() != nil {
		testingEventType = append(testingEventType, telemetry.HasCodeOwnerEventType...)
	}

	ciProviderName, hasCiProvider := utils.GetCITags()[constants.CIProviderName]
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
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if t.closed {
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

	setCIVisibilitySpanTag(t.span, constants.TestCommandExitCode, exitCode)
	if exitCode == 0 {
		setCIVisibilitySpanTag(t.span, constants.TestStatus, constants.TestStatusPass)
	} else {
		t.SetError(WithErrorInfo("ExitCode", "exit code is not zero.", ""))
		setCIVisibilitySpanTag(t.span, constants.TestStatus, constants.TestStatusFail)
	}
	t.efdAbortReasonMu.Lock()
	faultyEFDSession := t.efdAbortReason == "faulty"
	t.efdAbortReasonMu.Unlock()

	// Native startup can finish before asynchronous CI feature discovery.
	// Publish newly discovered capabilities and ITR correlation before sealing
	// the session event, preserving explicit session attributes.
	for key, value := range utils.GetCITags() {
		if strings.HasPrefix(key, "_dd.library_capabilities.") || key == constants.ItrCorrelationIDTag {
			if _, exists := t.GetTag(key); !exists {
				t.SetTag(key, value)
			}
		}
	}
	t.span.Finish(tracer.FinishTime(defaults.finishTime))
	t.closed = true

	// Creating telemetry event finished
	testingEventType := telemetry.SessionEventType
	if utils.GetCodeOwners() != nil {
		testingEventType = append(testingEventType, telemetry.HasCodeOwnerEventType...)
	}
	if _, hasCiProvider := utils.GetCITags()[constants.CIProviderName]; !hasCiProvider {
		testingEventType = append(testingEventType, telemetry.UnsupportedCiEventType...)
	}
	if faultyEFDSession {
		testingEventType = append(testingEventType, telemetry.EfdAbortFaultyEventType...)
	}
	telemetry.EventFinished(t.framework, testingEventType)
	tracer.Flush()
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
		mod = createTestModule(t, name, defaults.framework, defaults.frameworkVersion, defaults.startTime)
		t.modules[name] = mod
	}

	return mod
}
