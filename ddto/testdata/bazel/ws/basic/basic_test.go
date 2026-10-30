package basic

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) { os.Exit(m.Run()) }

func TestPass(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fatal("2 + 3")
	}
}

func TestFail(t *testing.T) { t.Errorf("intentional failure %d", 1) }

func TestSkip(t *testing.T) { t.Skip("intentional skip") }

func TestSub(t *testing.T) {
	t.Run("a", func(t *testing.T) {})
	t.Run("b", func(t *testing.T) {})
}

func TestParallel(t *testing.T) {
	for _, name := range []string{"x", "y"} {
		t.Run(name, func(t *testing.T) { t.Parallel() })
	}
}
