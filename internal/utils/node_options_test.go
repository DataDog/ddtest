package utils

import "testing"

func TestCIRequireOptions(t *testing.T) {
	for _, test := range []struct {
		name    string
		options string
		without string
		require string
		has     bool
	}{
		{name: "package name", options: "-r dd-trace/ci/init --max-old-space-size=256", without: "--max-old-space-size=256", require: "dd-trace/ci/init", has: true},
		{name: "absolute path", options: "--require=/tmp/node_modules/dd-trace/ci/init.js --trace-warnings", without: "--trace-warnings", require: "/tmp/node_modules/dd-trace/ci/init.js", has: true},
		{name: "quoted path and loader", options: `--require "/tmp/project loader.cjs" -r "/tmp/action install/dd-trace/ci/init.js" --max-old-space-size=256`, without: `--require "/tmp/project loader.cjs" --max-old-space-size=256`, require: "/tmp/action install/dd-trace/ci/init.js", has: true},
		{name: "attached short option", options: "-r/tmp/dd-trace/ci/init.js --trace-warnings", without: "--trace-warnings", require: "/tmp/dd-trace/ci/init.js", has: true},
		{name: "unrelated preload", options: "--require /tmp/unrelated/init.js --trace-warnings", without: "--require /tmp/unrelated/init.js --trace-warnings"},
		{name: "similar package", options: "-r dd-trace/ci/initializer", without: "-r dd-trace/ci/initializer"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := NodeOptionsHasRequire(test.options, "dd-trace/ci/init"); got != test.has {
				t.Fatalf("HasRequire() = %t, want %t", got, test.has)
			}
			if got := NodeOptionsRequire(test.options, "dd-trace/ci/init"); got != test.require {
				t.Fatalf("Require() = %q, want %q", got, test.require)
			}
			if got := NodeOptionsWithoutRequire(test.options, "dd-trace/ci/init"); got != test.without {
				t.Fatalf("WithoutRequire() = %q, want %q", got, test.without)
			}
		})
	}
}

func TestRegisterImportOptions(t *testing.T) {
	options := `--import="/tmp/action install/dd-trace/register.js" --require "/tmp/loader.cjs"`
	if !NodeOptionsHasImport(options, "dd-trace/register.js") {
		t.Fatal("absolute register import was not found")
	}
	if got := NodeOptionsWithoutImport(options, "dd-trace/register.js"); got != `--require "/tmp/loader.cjs"` {
		t.Fatalf("WithoutImport() = %q", got)
	}
}
