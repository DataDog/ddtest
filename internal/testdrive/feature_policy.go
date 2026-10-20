package testdrive

// A policy describes what must be exercised, never an assumed passing result.
// Browser adapters add their not-supported/deferred exceptions with their PRs.
type featurePolicy struct {
	Name   string
	Status string
	Reason string
}

var frameworkFeaturePolicies = map[string][]featurePolicy{
	"jest": {
		{Name: "auto-retries", Status: "required"},
		{Name: "early-flake-detection", Status: "required"},
		{Name: "skipping", Status: "required"},
		{Name: "quarantine", Status: "required"},
		{Name: "disabled", Status: "required"},
		{Name: "attempt-to-fix", Status: "required"},
	},
}

func supportsValidationFramework(name string) bool {
	_, ok := frameworkFeaturePolicies[name]
	return ok
}
func featureSatisfied(framework string, feature featureResult) bool {
	for _, policy := range frameworkFeaturePolicies[framework] {
		if policy.Name == feature.Name {
			if policy.Status == "required" {
				return feature.Status == "passed"
			}
			return feature.Status == policy.Status
		}
	}
	return false
}

func featureChecksPassed(framework string, features []featureResult) bool {
	if !supportsValidationFramework(framework) {
		return true
	}
	projects := map[string]map[string]bool{}
	for _, feature := range features {
		if !featureSatisfied(framework, feature) {
			return false
		}
		if projects[feature.Project] == nil {
			projects[feature.Project] = map[string]bool{}
		}
		projects[feature.Project][feature.Name] = true
	}
	if len(projects) == 0 {
		return false
	}
	for _, features := range projects {
		for _, policy := range frameworkFeaturePolicies[framework] {
			if !features[policy.Name] {
				return false
			}
		}
	}
	return true
}

func featurePolicies(framework string) []featurePolicy {
	if framework == "" {
		framework = "jest"
	}
	return frameworkFeaturePolicies[framework]
}
