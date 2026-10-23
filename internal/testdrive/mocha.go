package testdrive

// Execute the selected CI command unchanged. Installing a coverage tool alone
// is not permission to add it to the customer's command.
func (t *Testdrive) executionCommand() (string, []string) {
	return t.command, t.args
}
