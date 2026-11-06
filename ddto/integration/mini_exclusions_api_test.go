package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func checkMiniPublicMethodExclusions(t *testing.T) {
	dir, driver := prepareMiniFixture(t)
	installExternalTestifyHelper(t, dir)
	writeBuildFixture(t, dir, map[string]string{"api_helpers.go": `package fixture
import("os";"github.com/stretchr/testify/suite";"github.com/DataDog/ddtest/ddto/testopt")
type OrdinaryAPI struct{suite.Suite}
func(s OrdinaryAPI) TestCritical(){
 f,err:=os.OpenFile(os.Getenv("API_COUNTER"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{s.T().Fatal(err)}
 _,err=f.WriteString("x");f.Close();if err!=nil{s.T().Fatal(err)}
 s.T().Error("ordinary method must keep normal policy")
}
// This package function deliberately has a method's name and first parameter.
// Its registration must not register the unrelated method expression above.
func TestCritical(OrdinaryAPI){}
func init(){if os.Getenv("SKIP_PACKAGE_FUNCTION_REGISTRATION")!="true"{testopt.RegisterTestExclusions(TestCritical,testopt.TestExclusions{NoAutoRetry:true,PreventFailureSuppression:true})}}
`, "public_api_test.go": `package fixture_test
import("os";"testing";fixture "example.com/dd-ci-testing-fixture";"github.com/stretchr/testify/suite";"github.com/DataDog/ddtest/ddto/testopt")
func recordAPI(t *testing.T){
 f,err:=os.OpenFile(os.Getenv("API_COUNTER"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{t.Fatal(err)}
 _,err=f.WriteString("x");f.Close();if err!=nil{t.Fatal(err)}
 t.Error("API critical failure")
}
// Keep the ordinary control in the same module as the other suites while its
// declaring method and same-signature package function stay in the helper.
type OrdinaryAPI struct{fixture.OrdinaryAPI}
type ValueAPI struct{suite.Suite}
func(s ValueAPI) TestCritical(){recordAPI(s.T())}
type PointerAPI struct{suite.Suite}
func(s *PointerAPI) TestCritical(){recordAPI(s.T())}
type PromotedBaseAPI struct{suite.Suite}
func(s PromotedBaseAPI) TestCritical(){recordAPI(s.T())}
type PromotedAPI struct{PromotedBaseAPI}
type GenericAPI[T any] struct{suite.Suite}
func(s GenericAPI[T]) TestCritical(){recordAPI(s.T())}
func init(){
 flags:=testopt.TestExclusions{NoAutoRetry:true,PreventFailureSuppression:true}
 testopt.RegisterTestExclusions(ValueAPI.TestCritical,flags)
 testopt.RegisterTestExclusions((*PointerAPI).TestCritical,flags)
 testopt.RegisterTestExclusions(PromotedAPI.TestCritical,flags)
 testopt.RegisterTestExclusions(GenericAPI[int].TestCritical,flags)
}
func TestPublicAPI(t *testing.T){
 switch os.Getenv("API_KIND"){
 case "value":suite.Run(t,new(ValueAPI))
 case "pointer":suite.Run(t,new(PointerAPI))
 case "promoted":suite.Run(t,new(PromotedAPI))
 case "generic-int":suite.Run(t,new(GenericAPI[int]))
 case "generic-string":suite.Run(t,new(GenericAPI[string]))
 case "ordinary":suite.Run(t,new(OrdinaryAPI))
 default:t.Fatal("unknown API suite")
 }
}
`})
	bin := filepath.Join(t.TempDir(), executableName("public-api.test"))
	out, stderr, code := command(t, dir, testEnv("DD_CIVISIBILITY_ENABLED=false"), driver, exclusionBuildArgs(bin, true)...)
	if code != 0 {
		t.Fatalf("compile API fixture %d: %s %s", code, out, stderr)
	}
	for _, mode := range []string{"in_process", "process"} {
		for _, kind := range []string{"value", "pointer", "promoted", "generic-int", "generic-string", "ordinary"} {
			t.Run(mode+"-"+kind, func(t *testing.T) {
				suiteName := ""
				for _, quarantined := range []bool{false, true} {
					name := "normal"
					if quarantined {
						name = "quarantined"
					}
					t.Run(name, func(t *testing.T) {
						counter := filepath.Join(t.TempDir(), "calls")
						receiver, got := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestPublicAPI$"}, Policy: policySettings{Retry: true, Quarantined: quarantined, ManagementTarget: "TestPublicAPI/TestCritical", ManagementSuite: suiteName}, Env: []string{"API_KIND=" + kind, "API_COUNTER=" + counter, "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2"}})
						ordinary := kind == "ordinary"
						wantExit := 1
						if ordinary && quarantined {
							wantExit = 0
						}
						if got.code != wantExit {
							t.Fatalf("exit%d want%d: %s %s", got.code, wantExit, got.out, got.stderr)
						}
						data, err := os.ReadFile(counter)
						if err != nil && !(ordinary && quarantined && os.IsNotExist(err)) {
							t.Fatal(err)
						}
						if ordinary && quarantined {
							// Ordinary quarantine can suppress the body at the
							// selected management boundary. Compare the same
							// case without the package-function registration.
							baselineCounter := filepath.Join(t.TempDir(), "baseline-calls")
							baselineReceiver, baseline := runParityCase(t, dir, bin, parityCase{Args: []string{"-test.run=^TestPublicAPI$"}, Policy: policySettings{Retry: true, Quarantined: true, ManagementTarget: "TestPublicAPI/TestCritical", ManagementSuite: suiteName}, Env: []string{"API_KIND=" + kind, "API_COUNTER=" + baselineCounter, "SKIP_PACKAGE_FUNCTION_REGISTRATION=true", "DD_CIVISIBILITY_RETRY_EXECUTION_MODE=" + mode, "DD_CIVISIBILITY_FLAKY_RETRY_ENABLED=true", "DD_CIVISIBILITY_FLAKY_RETRY_COUNT=2"}})
							baselineData, baselineErr := os.ReadFile(baselineCounter)
							if baselineErr != nil && !os.IsNotExist(baselineErr) {
								t.Fatal(baselineErr)
							}
							if baseline.code != got.code || string(baselineData) != string(data) {
								t.Fatalf("package-function registration changed ordinary quarantine: callbacks=%q baseline=%q exit=%d baseline=%d", data, baselineData, got.code, baseline.code)
							}
							baselineEvents := 0
							for _, event := range baselineReceiver.events {
								if event["type"] != "test" {
									continue
								}
								meta := event["content"].(map[string]any)["meta"].(map[string]any)
								if meta["test.name"] != "TestPublicAPI/TestCritical" {
									continue
								}
								baselineEvents++
								if meta["test.test_management.no_auto_retry"] != nil || meta["test.test_management.prevent_failure_suppression"] != nil || meta["test.test_management.is_quarantined"] != "true" {
									t.Fatalf("ordinary quarantine baseline metadata: %v", meta)
								}
							}
							if baselineEvents == 0 {
								t.Fatal("missing ordinary quarantine baseline event")
							}
						}
						if !ordinary && string(data) != "x" {
							t.Fatalf("excluded method calls=%q want=x", data)
						}
						if ordinary && !quarantined && string(data) != "xxx" {
							t.Fatalf("ordinary method calls=%q want=xxx", data)
						}
						events := 0
						for _, event := range receiver.events {
							if event["type"] != "test" {
								continue
							}
							meta := event["content"].(map[string]any)["meta"].(map[string]any)
							if meta["test.name"] != "TestPublicAPI/TestCritical" {
								continue
							}
							events++
							actualSuite, _ := meta["test.suite"].(string)
							if !quarantined {
								suiteName = actualSuite
							}
							if ordinary {
								if meta["test.test_management.no_auto_retry"] != nil || meta["test.test_management.prevent_failure_suppression"] != nil {
									t.Fatalf("package function registration leaked to ordinary method: %v", meta)
								}
							} else {
								if meta["test.test_management.no_auto_retry"] != "true" || meta["test.test_management.prevent_failure_suppression"] != "true" || meta["test.status"] != "fail" {
									t.Fatalf("API exclusion lost: %v", meta)
								}
								for _, key := range []string{"test.is_retry", "test.retry_reason", "test.retry.execution_mode"} {
									if meta[key] != nil {
										t.Fatalf("excluded API method inherited retry: %v", meta)
									}
								}
							}
							if quarantined && meta["test.test_management.is_quarantined"] != "true" {
								t.Fatalf("raw backend assignment lost: %v", meta)
							}
						}
						if !ordinary && events != 1 {
							t.Fatalf("excluded API events=%d want=1", events)
						}
						if events == 0 || strings.TrimSpace(suiteName) == "" {
							t.Fatalf("missing actual method metadata: events=%d suite=%q", events, suiteName)
						}
					})
				}
			})
		}
	}
}
