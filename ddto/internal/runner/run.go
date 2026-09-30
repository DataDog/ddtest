package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/instrument"
)

const SDKVersion = "v2.11.0-rc.1"
const sdkPackage = "github.com/DataDog/dd-trace-go/v2/civisibility"

type goPackage struct {
	Dir, Name, ImportPath              string
	GoFiles, TestGoFiles, XTestGoFiles []string
	Module                             *struct {
		Path, Version string
		Replace       *struct{ Dir string }
	}
	Error *struct{ Err string }
}
type Overlay struct{ Replace map[string]string }

type Plan struct {
	File, Dir                       string
	InstrumentedFiles, TestPackages int
}

// Prepare creates a complete plan before native Go compilation starts. Callers
// own the plan directory and must remove it after all compiler processes finish.
func Prepare(ctx context.Context, dir string, args []string) (plan Plan, err error) {
	opts, err := parseOptions(args, os.Getenv("GOFLAGS"))
	if err != nil {
		return plan, err
	}
	replacements := map[string]string{}
	if opts.overlay != "" {
		path := opts.overlay
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return plan, e
		}
		var overlay Overlay
		if e = json.Unmarshal(data, &overlay); e != nil {
			return plan, e
		}
		for from, to := range overlay.Replace {
			if !filepath.IsAbs(from) {
				from = filepath.Join(dir, from)
			}
			if to != "" && !filepath.IsAbs(to) {
				to = filepath.Join(dir, to)
			}
			replacements[filepath.Clean(from)] = to
		}
	}
	listArgs := append([]string{"list", "-json=Dir,Name,ImportPath,GoFiles,TestGoFiles,XTestGoFiles,Module,Error"}, opts.buildFlags...)
	listArgs = append(listArgs, opts.packages...)
	listArgs = append(listArgs, "testing", sdkPackage)
	cmd := exec.CommandContext(ctx, "go", listArgs...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	data, e := cmd.Output()
	if e != nil {
		return plan, fmt.Errorf("resolve packages (SDK %s must already be required): %w\n%s", SDKVersion, e, stderr.String())
	}
	var packages []goPackage
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	for {
		var p goPackage
		e = decoder.Decode(&p)
		if e == io.EOF {
			break
		}
		if e != nil {
			return plan, e
		}
		if p.Error != nil {
			return plan, fmt.Errorf("%s: %s", p.ImportPath, p.Error.Err)
		}
		packages = append(packages, p)
	}
	var native *goPackage
	foundSDK := false
	for i := range packages {
		p := &packages[i]
		if p.ImportPath == "testing" {
			native = p
		}
		if p.ImportPath == sdkPackage {
			if p.Module == nil || p.Module.Version != SDKVersion || p.Module.Replace != nil {
				return plan, fmt.Errorf("POC requires unmodified dd-trace-go %s", SDKVersion)
			}
			foundSDK = true
		}
	}
	if !foundSDK || native == nil {
		return plan, fmt.Errorf("missing testing or SDK package")
	}
	files := map[string][]byte{}
	for _, file := range native.GoFiles {
		path := filepath.Join(native.Dir, file)
		actual := path
		if to, ok := replacements[path]; ok {
			actual = to
		}
		src, e := os.ReadFile(actual)
		if e != nil {
			return plan, e
		}
		files[path] = src
	}
	rewritten, e := instrument.Transform(files)
	if e != nil {
		return plan, e
	}
	temp, e := os.MkdirTemp("", "dd-ci-testing-poc-")
	if e != nil {
		return plan, e
	}
	plan.Dir = temp
	defer func() {
		if err != nil {
			_ = os.RemoveAll(temp)
		}
	}()
	add := func(logical, content string) error {
		if _, exists := replacements[logical]; exists {
			return fmt.Errorf("generated file conflicts with user overlay: %s", logical)
		}
		if _, e := os.Lstat(logical); e == nil {
			return fmt.Errorf("generated file already exists: %s", logical)
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		backing := filepath.Join(temp, fmt.Sprintf("generated-%d.go", len(replacements)))
		if e := os.WriteFile(backing, []byte(content), 0600); e != nil {
			return e
		}
		replacements[logical] = backing
		return nil
	}
	for logical, src := range rewritten {
		backing := filepath.Join(temp, filepath.Base(logical))
		if e = os.WriteFile(backing, src, 0600); e != nil {
			return plan, e
		}
		replacements[logical] = backing
	}
	if e = add(filepath.Join(native.Dir, "zz_dd_ci_visibility_hooks.go"), instrument.Hooks); e != nil {
		return plan, e
	}
	for _, p := range packages {
		if p.ImportPath == "testing" || p.ImportPath == sdkPackage || len(p.TestGoFiles)+len(p.XTestGoFiles) == 0 {
			continue
		}
		if p.Module == nil {
			return plan, fmt.Errorf("stdlib tests are outside this POC: %s", p.ImportPath)
		}
		content := "package " + p.Name + "_test\nimport _ " + fmt.Sprintf("%q", sdkPackage) + "\n"
		if e = add(filepath.Join(p.Dir, "zz_dd_ci_visibility_test.go"), content); e != nil {
			return plan, e
		}
		plan.TestPackages++
	}
	plan.File = filepath.Join(temp, "overlay.json")
	plan.InstrumentedFiles = len(rewritten)
	encoded, e := json.Marshal(Overlay{Replace: replacements})
	if e != nil {
		return plan, e
	}
	if e = os.WriteFile(plan.File, encoded, 0600); e != nil {
		return plan, e
	}
	return plan, nil
}

// Run retains native test output, flags, working directory, and exit status.
// It deliberately preserves the user's test-result caching choice.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	plan, err := Prepare(ctx, dir, args)
	if err != nil {
		fmt.Fprintln(stderr, "ddtest:", err)
		return 2
	}
	defer os.RemoveAll(plan.Dir)
	forwarded := []string{"test", "-overlay=" + plan.File}
	// The merged overlay replaces only the user's overlay flag, never test flags.
	for i := 0; i < len(args); i++ {
		if args[i] == "-args" {
			forwarded = append(forwarded, args[i:]...)
			break
		}
		if args[i] == "-overlay" {
			i++
			continue
		}
		if strings.HasPrefix(args[i], "-overlay=") {
			continue
		}
		forwarded = append(forwarded, args[i])
	}
	cmd := exec.CommandContext(ctx, "go", forwarded...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	cmd.WaitDelay = 5 * time.Second
	if err = cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		fmt.Fprintln(stderr, "ddtest:", err)
		return 2
	}
	return 0
}
