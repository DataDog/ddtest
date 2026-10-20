package onboard

import (
	"strings"
)

func legacyCodecovUpload(command string) bool {
	const prefix = "bash <(curl -s https://codecov.io/bash)"
	if !strings.HasPrefix(strings.TrimSpace(command), prefix) {
		return false
	}
	words, err := staticCommands(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(command), prefix)))
	if err != nil || len(words) > 1 {
		return false
	}
	if len(words) == 0 {
		return true
	}
	for i := 0; i < len(words[0]); i += 2 {
		if i+1 >= len(words[0]) || (words[0][i] != "-F" && words[0][i] != "-s") {
			return false
		}
	}
	return true
}
