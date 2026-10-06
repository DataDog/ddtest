// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations"
	"os"
	"reflect"
	"runtime"
	"testing"
	"time"
)

type processRetryRecordingEvent struct {
	tags         map[string]any
	errorType    string
	errorMessage string
	errorStack   string
}

func (e *processRetryRecordingEvent) Context() context.Context { return context.Background() }
func (e *processRetryRecordingEvent) StartTime() time.Time     { return time.Time{} }
func (e *processRetryRecordingEvent) SetError(options ...integrations.ErrorOption) {
	e.SetTag("error", true)
	for _, option := range options {
		e.errorType = processRetryOptionStringField(option, "errType")
		e.errorMessage = processRetryOptionStringField(option, "message")
		e.errorStack = processRetryOptionStringField(option, "callstack")
	}
}
func (e *processRetryRecordingEvent) SetTag(key string, value any) {
	if e.tags == nil {
		e.tags = map[string]any{}
	}
	e.tags[key] = value
}
func (e *processRetryRecordingEvent) GetTag(key string) (any, bool) {
	value, ok := e.tags[key]
	return value, ok
}

func requireProcessRetryTagsExclude(t testing.TB, tags map[string]any, forbidden ...string) {
	t.Helper()
	for key, value := range tags {
		valueString := fmt.Sprint(value)
		for _, sentinel := range forbidden {
			require.NotContains(t, valueString, sentinel, "tag %q contains forbidden sentinel", key)
		}
	}
}

func requireProcessRetryFileMode(t testing.TB, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, want, info.Mode().Perm())
}

var _ integrations.TestSession = (*processRetryRecordingSession)(nil)

type processRetryRecordingSession struct {
	processRetryRecordingEvent
	modules    map[string]*processRetryRecordingModule
	tests      []*processRetryRecordingTest
	closeCount int
}

func (s *processRetryRecordingSession) SessionID() uint64        { return 1 }
func (s *processRetryRecordingSession) Command() string          { return "go test" }
func (s *processRetryRecordingSession) Framework() string        { return "go" }
func (s *processRetryRecordingSession) WorkingDirectory() string { return "." }
func (s *processRetryRecordingSession) Close(int, ...integrations.TestSessionCloseOption) {
	s.closeCount++
}
func (s *processRetryRecordingSession) GetOrCreateModule(name string, _ ...integrations.TestModuleStartOption) integrations.TestModule {
	if s.modules == nil {
		s.modules = map[string]*processRetryRecordingModule{}
	}
	if module := s.modules[name]; module != nil {
		return module
	}
	module := &processRetryRecordingModule{session: s, name: name, suites: map[string]*processRetryRecordingSuite{}}
	s.modules[name] = module
	return module
}

var _ integrations.TestModule = (*processRetryRecordingModule)(nil)

type processRetryRecordingModule struct {
	processRetryRecordingEvent
	session    *processRetryRecordingSession
	name       string
	suites     map[string]*processRetryRecordingSuite
	closeCount int
}

func (m *processRetryRecordingModule) ModuleID() uint64                  { return 2 }
func (m *processRetryRecordingModule) Session() integrations.TestSession { return m.session }
func (m *processRetryRecordingModule) Framework() string                 { return "go" }
func (m *processRetryRecordingModule) Name() string                      { return m.name }
func (m *processRetryRecordingModule) Close(...integrations.TestModuleCloseOption) {
	m.closeCount++
}
func (m *processRetryRecordingModule) GetOrCreateSuite(name string, _ ...integrations.TestSuiteStartOption) integrations.TestSuite {
	if m.suites == nil {
		m.suites = map[string]*processRetryRecordingSuite{}
	}
	if suite := m.suites[name]; suite != nil {
		return suite
	}
	suite := &processRetryRecordingSuite{module: m, name: name}
	m.suites[name] = suite
	return suite
}

var _ integrations.TestSuite = (*processRetryRecordingSuite)(nil)

type processRetryRecordingSuite struct {
	processRetryRecordingEvent
	module     *processRetryRecordingModule
	name       string
	closeCount int
}

func (s *processRetryRecordingSuite) SuiteID() uint64                 { return 3 }
func (s *processRetryRecordingSuite) Module() integrations.TestModule { return s.module }
func (s *processRetryRecordingSuite) Name() string                    { return s.name }
func (s *processRetryRecordingSuite) Close(...integrations.TestSuiteCloseOption) {
	s.closeCount++
}
func (s *processRetryRecordingSuite) CreateTest(name string, _ ...integrations.TestStartOption) integrations.Test {
	test := &processRetryRecordingTest{suite: s, name: name}
	s.module.session.tests = append(s.module.session.tests, test)
	return test
}

var _ integrations.Test = (*processRetryRecordingTest)(nil)

type processRetryRecordingTest struct {
	processRetryRecordingEvent
	suite      *processRetryRecordingSuite
	name       string
	status     processRetryStatus
	logs       []string
	skipReason string
	closeCount int
}

func (t *processRetryRecordingTest) TestID() uint64                          { return 4 }
func (t *processRetryRecordingTest) Name() string                            { return t.name }
func (t *processRetryRecordingTest) Suite() integrations.TestSuite           { return t.suite }
func (t *processRetryRecordingTest) SetTestFunc(*runtime.Func)               {}
func (t *processRetryRecordingTest) SetBenchmarkData(string, map[string]any) {}
func (t *processRetryRecordingTest) Log(message, _ string) {
	t.logs = append(t.logs, message)
}
func (t *processRetryRecordingTest) Close(status integrations.TestResultStatus, options ...integrations.TestCloseOption) {
	t.closeCount++
	for _, option := range options {
		if skipReason := processRetryOptionStringField(option, "skipReason"); skipReason != "" {
			t.skipReason = skipReason
		}
	}
	switch status {
	case integrations.ResultStatusPass:
		t.status = processRetryStatusPass
	case integrations.ResultStatusSkip:
		t.status = processRetryStatusSkip
	default:
		t.status = processRetryStatusFail
	}
}

func newProcessRetryRecordingTestForTesting(name string) *processRetryRecordingTest {
	session := &processRetryRecordingSession{}
	module := &processRetryRecordingModule{session: session}
	suite := &processRetryRecordingSuite{module: module}
	return &processRetryRecordingTest{
		suite: suite,
		name:  name,
	}
}

func processRetryOptionStringField(option any, fieldName string) string {
	fn := reflect.ValueOf(option)
	if !fn.IsValid() || fn.Kind() != reflect.Func || fn.Type().NumIn() != 1 || fn.Type().In(0).Kind() != reflect.Pointer {
		return ""
	}
	argument := reflect.New(fn.Type().In(0).Elem())
	fn.Call([]reflect.Value{argument})
	field := argument.Elem().FieldByName(fieldName)
	if !field.IsValid() || field.Kind() != reflect.String {
		return ""
	}
	return field.String()
}

func TestExtractedCIPolicySelectionAndITR(t *testing.T) {
	exerciseAdditionalFeaturePathSelection(t)
	exerciseParallelEFDSelection(t)
	exerciseMetadataOnlyPropagationSuppression(t)
	exerciseSlowEFDAbortTagging(t)
	exerciseITRCoverageBackfillState(t)
	exerciseNarrowingFlagParsing(t)
}
