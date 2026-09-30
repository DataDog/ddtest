package runner

import (
	"reflect"
	"testing"
)

func TestPackageAndFlagSelection(t *testing.T) {
	cases := []struct{ args, packages, flags []string }{
		{[]string{"-race", "-tags", "integration", "-run", "TestOne", "./...", "-args", "./not-a-package"}, []string{"./..."}, []string{"-race", "-tags", "integration"}},
		{[]string{"./one", "-count=2", "./two", "-covermode=atomic", "-cover"}, []string{"./one", "./two"}, []string{"-covermode=atomic", "-cover"}},
		{[]string{"-json", "-timeout", "10s"}, []string{"."}, nil},
		{[]string{"-test.v", "-test.run=TestPass", "."}, []string{"."}, nil},
	}
	for _, c := range cases {
		got, err := parseOptions(c.args, "")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.packages, c.packages) || !reflect.DeepEqual(got.buildFlags, c.flags) {
			t.Fatalf("%v: packages %v, flags %v", c.args, got.packages, got.buildFlags)
		}
	}
}
func TestRejectConflictingToolExec(t *testing.T) {
	for _, args := range [][]string{{"-toolexec=orchestrion toolexec"}, {"-toolexec", "other"}} {
		if _, err := parseOptions(args, ""); err == nil {
			t.Fatal("conflicting toolexec accepted")
		}
	}
	if _, err := parseOptions(nil, `-toolexec="orchestrion toolexec"`); err == nil {
		t.Fatal("GOFLAGS conflict accepted")
	}
}
func TestRejectAmbiguousArguments(t *testing.T) {
	for _, args := range [][]string{{"-tags"}, {"file_test.go"}, {"-custom"}} {
		if _, err := parseOptions(args, ""); err == nil {
			t.Fatalf("ambiguous arguments accepted: %v", args)
		}
	}
}
