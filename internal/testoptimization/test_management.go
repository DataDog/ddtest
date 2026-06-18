package testoptimization

import "github.com/DataDog/ddtest/internal/testoptimization/api"

func DisabledTestsFromTestManagementData(testManagementTests *api.TestManagementTestsResponseDataModules) map[string]bool {
	disabledTests := make(map[string]bool)
	if testManagementTests == nil {
		return disabledTests
	}

	for module, suites := range testManagementTests.Modules {
		for suite, tests := range suites.Suites {
			for name, test := range tests.Tests {
				if !test.Properties.Disabled || test.Properties.AttemptToFix {
					continue
				}
				disabledTest := Test{
					Module: module,
					Suite:  suite,
					Name:   name,
				}
				disabledTests[disabledTest.FQN()] = true
			}
		}
	}

	return disabledTests
}
