package testdrive

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Prepared shares repeated evidence in large reports. Detail placeholders are
// hydrated on expansion; the standalone HTML retains every source and file list.
func (model reportModel) Prepared() reportModel {
	if len(model.Tests) < 500 {
		return model
	}
	sources := map[string]string{}
	coverages := map[string]string{}
	shareSource := func(source reportSource) reportSource {
		data, _ := json.Marshal(source)
		key := string(data)
		reference, exists := sources[key]
		if !exists {
			reference = fmt.Sprintf("shared-source-%d", len(model.SharedSources))
			sources[key] = reference
			model.SharedSources = append(model.SharedSources, source)
		}
		return reportSource{Reference: reference}
	}
	shareFiles := func(files []string) string {
		if len(files) == 0 {
			return ""
		}
		key := strings.Join(files, "\x00")
		reference, exists := coverages[key]
		if !exists {
			reference = fmt.Sprintf("shared-coverage-%d", len(model.SharedCoverage))
			coverages[key] = reference
			model.SharedCoverage = append(model.SharedCoverage, files)
		}
		return reference
	}
	prepareTests := func(tests []reportTest) []reportTest {
		tests = slices.Clone(tests)
		for i := range tests {
			tests[i].Source = shareSource(tests[i].Source)
			tests[i].CoverageReference = shareFiles(tests[i].CoveredFiles)
		}
		return tests
	}
	model.Tests = prepareTests(model.Tests)
	model.Cards = slices.Clone(model.Cards)
	for i := range model.Cards {
		model.Cards[i].Tests = prepareTests(model.Cards[i].Tests)
	}
	return model
}
