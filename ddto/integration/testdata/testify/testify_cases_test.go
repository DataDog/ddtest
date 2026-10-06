package fixture_test

import (
	"os"
	"reflect"
	"testing"

	"example.com/dd-ci-testing-fixture/helpers"
	"github.com/stretchr/testify/suite"
)

type ParitySuite struct{ suite.Suite }

func (s *ParitySuite) TestPass()           { s.True(true); s.Require().NoError(nil) }
func (s *ParitySuite) TestSkip()           { s.T().Skip("suite skip") }
func (s *ParitySuite) TestNested()         { s.Run("child", func() { s.Equal(1, 1) }) }
func (s *ParitySuite) TestAssertFailure()  { s.Equal(1, 2, "expected assertion failure") }
func (s *ParitySuite) TestRequireFailure() { s.Require().Equal(1, 2, "expected require failure") }
func (s *ParitySuite) TestPanic()          { panic("expected suite panic") }
func (s *ParitySuite) TestManaged()        { s.T().Error("managed suite failure") }
func (s *ParitySuite) TestFlaky() {
	f, err := os.OpenFile(os.Getenv("POC_RETRY_COUNTER"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		f.Close()
		s.T().Error("first suite attempt fails")
	} else if !os.IsExist(err) {
		s.T().Fatal(err)
	}
}
func TestParitySuite(t *testing.T)     { suite.Run(t, new(ParitySuite)) }
func TestParityHelper(t *testing.T)    { helpers.Run(t, new(ParitySuite)) }
func TestParityParallelA(t *testing.T) { t.Parallel(); suite.Run(t, new(ParitySuite)) }
func TestParityParallelB(t *testing.T) { t.Parallel(); suite.Run(t, new(ParitySuite)) }

type LifecycleSuite struct {
	suite.Suite
	events []string
	stats  *suite.SuiteInformation
}

func (s *LifecycleSuite) SetupSuite()                                         { s.events = append(s.events, "setup-suite") }
func (s *LifecycleSuite) TearDownSuite()                                      { s.events = append(s.events, "teardown-suite") }
func (s *LifecycleSuite) SetupTest()                                          { s.events = append(s.events, "setup-test") }
func (s *LifecycleSuite) TearDownTest()                                       { s.events = append(s.events, "teardown-test") }
func (s *LifecycleSuite) BeforeTest(_, name string)                           { s.events = append(s.events, "before:"+name) }
func (s *LifecycleSuite) AfterTest(_, name string)                            { s.events = append(s.events, "after:"+name) }
func (s *LifecycleSuite) SetupSubTest()                                       { s.events = append(s.events, "setup-child") }
func (s *LifecycleSuite) TearDownSubTest()                                    { s.events = append(s.events, "teardown-child") }
func (s *LifecycleSuite) HandleStats(_ string, stats *suite.SuiteInformation) { s.stats = stats }
func (s *LifecycleSuite) TestAlpha() {
	s.events = append(s.events, "alpha")
	s.Assert().True(true)
	s.Require().NoError(nil)
}
func (s *LifecycleSuite) TestBeta() {
	s.Run("child", func() { s.events = append(s.events, "child"); s.True(true) })
}
func TestParityLifecycle(t *testing.T) {
	s := new(LifecycleSuite)
	suite.Run(t, s)
	want := []string{"setup-suite", "setup-test", "before:TestAlpha", "alpha", "after:TestAlpha", "teardown-test", "setup-test", "before:TestBeta", "setup-child", "child", "teardown-child", "after:TestBeta", "teardown-test", "teardown-suite"}
	if !reflect.DeepEqual(s.events, want) || s.T() != t {
		t.Errorf("suite lifecycle/T: %v", s.events)
	}
	if s.stats == nil || s.stats.Start.IsZero() || s.stats.End.IsZero() || len(s.stats.TestStats) != 2 {
		t.Fatal("suite stats missing")
	}
	for name, stats := range s.stats.TestStats {
		if stats.Start.IsZero() || stats.End.IsZero() || !stats.Passed {
			t.Error("invalid method stats", name)
		}
	}
}

type CustomSuite struct {
	suite.Suite
	called bool
}

func (s *CustomSuite) Run(name string, f func()) bool {
	s.called = true
	return s.T().Run(name, func(t *testing.T) { f() })
}
func TestParityCustomRun(t *testing.T) {
	s := new(CustomSuite)
	s.SetT(t)
	s.Run("custom", func() {})
	if !s.called {
		t.Fatal("custom Run was replaced")
	}
}
func TestParityShadowedAlias(t *testing.T) {
	called := false
	suite := struct{ Run func() }{Run: func() { called = true }}
	suite.Run()
	if !called {
		t.Fatal("shadowed alias was replaced")
	}
}
