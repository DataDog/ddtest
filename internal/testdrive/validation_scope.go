package testdrive

func hasValidationPair(result validationResult) bool {
	if result.CheckOnly {
		return false
	}
	baseline, reporting := false, false
	for _, run := range result.Runs {
		if run.Command == "" || run.ExitCode == nil {
			continue
		}
		baseline = baseline || run.Role == roleBaseline && !run.Instrumented
		reporting = reporting || run.Role == roleReporting && run.Instrumented
	}
	return baseline && reporting
}
