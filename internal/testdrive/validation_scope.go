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
		baseline = baseline || run.Name == "baseline" && !run.Instrumented
		reporting = reporting || run.Name == "reporting-only" && run.Instrumented
	}
	return baseline && reporting
}
