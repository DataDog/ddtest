package platform

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/ddtest/internal/framework"
	"github.com/DataDog/ddtest/internal/settings"
)

func (r *Ruby) DetectFramework(root, hint string) (framework.Framework, error) {
	if root == "" {
		root = "."
	}
	if hint == "" {
		hint = settings.GetFramework()
	}
	candidates := []framework.Framework{framework.NewRSpec(), framework.NewMinitest()}
	if hint == "" {
		candidates = nil
		gemfile, err := os.ReadFile(filepath.Join(root, "Gemfile"))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		rspec, err := detectAnyFile(root, ".rspec")
		if err != nil {
			return nil, err
		}
		if rspec || strings.Contains(string(gemfile), "rspec") {
			candidates = append(candidates, framework.NewRSpec())
		}
		tests, err := os.Stat(filepath.Join(root, "test"))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err == nil && tests.IsDir() || strings.Contains(string(gemfile), "minitest") {
			candidates = append(candidates, framework.NewMinitest())
		}
	}
	fw, err := selectFramework(r.Name(), hint, candidates)
	if err != nil {
		return nil, err
	}
	fw.SetPlatformEnv(r.GetPlatformEnv())
	return fw, nil
}
