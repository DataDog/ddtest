package runner

import (
	"reflect"
	"testing"
)

func TestParseRuntimeArgs(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args, want []string
		runtime    Runtime
	}{
		{"default", []string{"-count", "1", "./..."}, []string{"-count", "1", "./..."}, Mini},
		{"first", []string{"--runtime=sdk", "-count=1"}, []string{"-count=1"}, SDK},
		{"after-count", []string{"-count", "1", "--runtime=mini"}, []string{"-count", "1"}, Mini},
		{"double-dash-count", []string{"--count", "1", "--runtime=sdk"}, []string{"--count", "1"}, SDK},
		{"separate-value", []string{"-race", "--runtime", "mini", "."}, []string{"-race", "."}, Mini},
		{"after-package", []string{"./...", "--runtime", "sdk", "-count=1"}, []string{"./...", "-count=1"}, SDK},
		{"before-chdir", []string{"--runtime=mini", "-C", "module", "."}, []string{"-C", "module", "."}, Mini},
		{"after-chdir", []string{"-C", "module", "--runtime=sdk", "."}, []string{"-C", "module", "."}, SDK},
		{"last-value", []string{"--runtime=mini", "--runtime", "sdk"}, []string{}, SDK},
		{"flag-value", []string{"-run", "--runtime=sdk", "."}, []string{"-run", "--runtime=sdk", "."}, Mini},
		{"test-flag-value", []string{"--test.run", "--runtime=sdk"}, []string{"--test.run", "--runtime=sdk"}, Mini},
		{"args", []string{"-args", "--runtime=sdk"}, []string{"-args", "--runtime=sdk"}, Mini},
		{"double-args", []string{"--args", "--runtime=sdk"}, []string{"--args", "--runtime=sdk"}, Mini},
		{"terminator", []string{"--", "--runtime=sdk"}, []string{"--", "--runtime=sdk"}, Mini},
		{"custom-value", []string{"-custom", "--runtime=sdk"}, []string{"-custom", "--runtime=sdk"}, Mini},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := append([]string(nil), tc.args...)
			gotRuntime, got, err := ParseRuntimeArgs(tc.args)
			if err != nil || gotRuntime != tc.runtime || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q %q %v; want %q %q", gotRuntime, got, err, tc.runtime, tc.want)
			}
			if !reflect.DeepEqual(before, tc.args) {
				t.Fatal("modified caller arguments")
			}
		})
	}
}

func TestParseRuntimeArgsRejectsInvalidValues(t *testing.T) {
	for _, args := range [][]string{
		{"--runtime"}, {"--runtime="}, {"--runtime=unknown"},
		{"-count", "1", "--runtime", "unknown"}, {"--runtime", "-count=1"},
	} {
		if _, _, err := ParseRuntimeArgs(args); err == nil {
			t.Errorf("accepted %q", args)
		}
	}
}
