package testoptimization

import "github.com/DataDog/ddtest/internal/utils/net"

func DisabledTestsFromTestManagementData(testManagementTests *net.TestManagementTestsResponseDataModules) map[string]bool {
	disabledTests := make(map[string]bool)
	if testManagementTests == nil {
		return disabledTests
	}

	for module, suites := range testManagementTests.Modules {
		for suite, tests := range suites.Suites {
			for name, test := range tests.Tests {
				if !test.Properties.Disabled {
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
