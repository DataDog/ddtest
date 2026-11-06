package onboard

// SelectTracerRelease lets the language resolve its selector to the release
// its setup pins and its resumed validation checks.
func SelectTracerRelease(language, requested string) (string, error) {
	if selector, ok := discoveryLanguageNamed(language).(interface{ selectTracerRelease(string) (string, error) }); ok {
		return selector.selectTracerRelease(requested)
	}
	return requested, nil
}
