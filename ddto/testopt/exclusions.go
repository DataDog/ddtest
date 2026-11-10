package testopt

import "github.com/DataDog/ddtest/ddto/internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting"

// TestExclusions describes execution controls on an individual test declaration.
// These controls preserve normal test selection, native skips and native retries.
type TestExclusions = gotesting.TestExclusions

// RegisterTestExclusions registers a test function or Testify method expression.
// Call it during package initialization, before TestMain or any tests execute.
// ddtest instrument generates these calls for //ddtest:no_auto_retry and
// //ddtest:prevent_failure_suppression comments on test declarations.
func RegisterTestExclusions(fn any, exclusions TestExclusions) {
	gotesting.RegisterTestExclusions(fn, exclusions)
}
