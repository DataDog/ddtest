package bazelupload

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

const sidecarName = "bazel_target_metadata.json"

// readSidecars fills the metadata and context of each source that names its
// output directory, reading the sidecars in parallel. It follows the rules
// uploader's read_bazel_metadata and ContextPlan.select, and records their
// warnings on the source. It stops reading once ctx is cancelled: the upload
// then prepares no further source, and reports the rest as cancelled.
func readSidecars(ctx context.Context, root *os.Root, plan *Plan, results []FileResult, workers int) {
	indexes := make(chan int)
	var group sync.WaitGroup
	for range max(1, workers) {
		group.Go(func() {
			for index := range indexes {
				source := &plan.Sources[index]
				metadata, warning := readSidecar(root, source.Output)
				if warning != "" {
					results[index].warn(warning)
				}
				source.Metadata = metadata
				repo, _ := metadata["bazel.test_optimization.repo_name"].(string)
				context, selectionWarning := plan.ContextSelection.Choose(repo)
				if selectionWarning != "" {
					results[index].warn(selectionWarning)
				}
				source.Context = context
			}
		})
	}
schedule:
	for index, source := range plan.Sources {
		if source.Output == "" {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		select {
		case indexes <- index:
		case <-ctx.Done():
			break schedule
		}
	}
	close(indexes)
	group.Wait()
}

// ReadSidecar reads the sidecar of the output directory dir as the upload
// does for a source that names its output: the metadata, or a warning code.
func ReadSidecar(dir string) (Object, string) {
	root, err := os.OpenRoot(filepath.Dir(dir))
	if err != nil {
		return nil, ""
	}
	defer func() { _ = root.Close() }()
	return readSidecar(root, filepath.Base(dir))
}

// readSidecar reads an output's sidecar as read_bazel_metadata does: a missing
// one gives no metadata and no warning, a link is unsafe, and one that is not
// a JSON object is invalid. Numbers read as Python's json module writes them
// back, so that the enriched events do not change.
func readSidecar(root *os.Root, output string) (Object, string) {
	// Opening the output once serves both lookups: each lookup through a
	// root opens every directory on its path.
	directory, err := root.OpenRoot(output)
	if err != nil {
		return nil, ""
	}
	defer directory.Close()
	info, err := directory.Lstat(sidecarName)
	if err != nil {
		return nil, ""
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		// Python follows the link, wherever it leads, to decide that the
		// sidecar is a file before it refuses the link.
		target, err := os.Stat(filepath.Join(root.Name(), output, sidecarName))
		if err == nil && target.Mode().IsRegular() {
			return nil, "bazel_metadata_unsafe"
		}
		return nil, ""
	}
	if !info.Mode().IsRegular() {
		return nil, ""
	}
	raw, err := directory.ReadFile(sidecarName)
	if err != nil {
		return nil, "bazel_metadata_invalid"
	}
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(raw) {
		return nil, "bazel_metadata_invalid"
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, "bazel_metadata_invalid"
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		return nil, "bazel_metadata_invalid"
	}
	metadata, ok := value.(map[string]any)
	if !ok {
		return nil, "bazel_metadata_invalid"
	}
	return pythonNumbers(metadata).(map[string]any), ""
}

// pythonNumbers rewrites every number as json.dumps writes the value that
// json.loads read from it: an integer in canonical form, and a float as
// Python's repr.
func pythonNumbers(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			typed[key] = pythonNumbers(item)
		}
	case []any:
		for i, item := range typed {
			typed[i] = pythonNumbers(item)
		}
	case json.Number:
		return json.Number(pythonNumber(string(typed)))
	}
	return value
}

func pythonNumber(literal string) string {
	if !strings.ContainsAny(literal, ".eE") {
		integer, ok := new(big.Int).SetString(literal, 10)
		if !ok {
			return literal
		}
		return integer.String()
	}
	// ParseFloat rounds an underflow to zero, as Python does; Python reads an
	// overflow as infinity, which a strict plan cannot carry, so it stays.
	value, err := strconv.ParseFloat(literal, 64)
	if math.IsInf(value, 0) || err != nil && value != 0 {
		return literal
	}
	return pythonFloat(value)
}

// pythonFloat is repr(float): the shortest round-tripping digits, in fixed
// notation with at least one decimal for exponents from -4 to 15, and in
// exponent notation with a signed exponent of at least two digits otherwise.
func pythonFloat(value float64) string {
	formatted := strconv.FormatFloat(value, 'e', -1, 64)
	sign := ""
	if strings.HasPrefix(formatted, "-") {
		sign, formatted = "-", formatted[1:]
	}
	mantissa, exponentText, _ := strings.Cut(formatted, "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	exponent, _ := strconv.Atoi(exponentText)
	if exponent >= -4 && exponent < 16 {
		if exponent < 0 {
			return sign + "0." + strings.Repeat("0", -exponent-1) + digits
		}
		if len(digits) <= exponent+1 {
			return sign + digits + strings.Repeat("0", exponent+1-len(digits)) + ".0"
		}
		return sign + digits[:exponent+1] + "." + digits[exponent+1:]
	}
	if len(digits) > 1 {
		digits = digits[:1] + "." + digits[1:]
	}
	exponentSign := "+"
	if exponent < 0 {
		exponentSign, exponent = "-", -exponent
	}
	return sign + digits + "e" + exponentSign + fmt.Sprintf("%02d", exponent)
}
