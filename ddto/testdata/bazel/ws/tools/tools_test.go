package tools

import (
	"os"
	"os/exec"
	"testing"
)

const want = "tool testing.Testing()=false\n"

func TestExecTool(t *testing.T) {
	data, err := os.ReadFile(os.Getenv("GEN"))
	if err != nil || string(data) != want {
		t.Fatalf("%q, %v", data, err)
	}
}

func TestDataTool(t *testing.T) {
	out, err := exec.Command(os.Getenv("TOOL")).Output()
	if err != nil || string(out) != want {
		t.Fatalf("%q, %v", out, err)
	}
}
