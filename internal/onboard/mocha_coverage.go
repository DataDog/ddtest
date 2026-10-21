package onboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
)

var istanbulPlugin = regexp.MustCompile(`['"](?:babel-plugin-)?istanbul['"]`)

// This is static configuration evidence, not proof that Babel instrumented the
// executed source. Real-suite coverage must still be observed by testdrive.
func MochaBabelIstanbulConfigured(root string) bool {
	for _, name := range []string{".babelrc", ".babelrc.json", ".babelrc.js", ".babelrc.cjs", "babel.config.js", "babel.config.cjs", "babel.config.json"} {
		path := filepath.Join(root, name)
		if info, err := os.Stat(path); err == nil && info.Size() <= 1<<20 {
			if data, err := os.ReadFile(path); err == nil && istanbulPlugin.Match(data) {
				return true
			}
		}
	}
	var manifest struct{ Babel json.RawMessage }
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	return err == nil && json.Unmarshal(data, &manifest) == nil && istanbulPlugin.Match(manifest.Babel)
}
