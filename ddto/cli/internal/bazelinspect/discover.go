package bazelinspect

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
)

// DiscoverRequest lists the testlogs roots that the rules_test_optimization
// uploader scans, after it resolved and deduplicated them, and the target
// roots of the targets that its doctor expects.
type DiscoverRequest struct {
	Version          int        `json:"schema_version"`
	ScanRoots        []ScanRoot `json:"scan_roots"`
	MaxDepth         int        `json:"max_depth"`
	StagedOutputKeys []string   `json:"staged_output_keys"`
	TargetRoots      []string   `json:"target_roots,omitempty"`
}

// ScanRoot is a local testlogs tree, or one that BEP artifact staging wrote.
type ScanRoot struct {
	Path   string `json:"path"`
	Staged bool   `json:"staged"`
}

// Discovery is what the uploader's discover_file_tasks finds: the selected
// outputs ordered by key, their payload sources in the uploader's order, and
// the warnings in the order that it raises them.
type Discovery struct {
	Version  int                `json:"schema_version"`
	Outputs  []DiscoveredOutput `json:"outputs"`
	Sources  []DiscoveredSource `json:"sources"`
	Warnings []string           `json:"warnings"`
	// Targets follow the request's target roots, in its order.
	Targets []DiscoveredTarget `json:"targets,omitempty"`
}

// DiscoveredTarget lists the outputs below one target root, as the doctor's
// find_test_output_directories(root, root_is_target=True) does: the root
// itself is never an output. Exists is Path.exists() for the root.
type DiscoveredTarget struct {
	Root    string   `json:"root"`
	Exists  bool     `json:"exists"`
	Outputs []string `json:"outputs"`
}

// DiscoveredOutput is one selected test.outputs directory.
type DiscoveredOutput struct {
	Path      string `json:"path"`
	OutputKey string `json:"output_key"`
	ScanRoot  string `json:"scan_root"`
	Staged    bool   `json:"staged"`
}

// DiscoveredSource is a payload file, named within payloads/<directory> of
// the output at index Output.
type DiscoveredSource struct {
	Output      int    `json:"output"`
	PayloadType string `json:"payload_type"`
	Directory   string `json:"directory"`
	Name        string `json:"name"`
}

var (
	retryOutputDirectory = regexp.MustCompile(`^attempt_[0-9]+[.]outputs$`)
	payloadDirectories   = [...]struct{ kind, name string }{{"test", "tests"}, {"coverage", "coverage"}, {"telemetry", "telemetry"}}
)

// Discover finds the outputs and payload sources below the scan roots as the
// uploader's discover_file_tasks does, walking and listing in parallel.
func Discover(request DiscoverRequest, workers int) Discovery {
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	discovery := Discovery{Version: 1, Outputs: []DiscoveredOutput{}, Sources: []DiscoveredSource{}, Warnings: []string{}}
	byKey := map[string][]DiscoveredOutput{}
	for _, root := range request.ScanRoots {
		for _, output := range findTestOutputDirectories(root.Path, request.MaxDepth, workers, false) {
			key := canonicalOutputKey(relativeKey(root.Path, output))
			byKey[key] = append(byKey[key], DiscoveredOutput{Path: output, OutputKey: key, ScanRoot: root.Path, Staged: root.Staged})
		}
	}
	staged := map[string]bool{}
	for _, key := range request.StagedOutputKeys {
		if key != "" {
			staged[strings.TrimLeft(strings.ReplaceAll(key, `\`, "/"), "/")] = true
		}
	}
	var warnings []string
	for _, key := range slices.Sorted(func(yield func(string) bool) {
		for key := range byKey {
			if !yield(key) {
				return
			}
		}
	}) {
		candidates := byKey[key]
		if !staged[key] {
			discovery.Outputs = append(discovery.Outputs, candidates[0])
			continue
		}
		index := slices.IndexFunc(candidates, func(output DiscoveredOutput) bool { return output.Staged })
		if index < 0 {
			warnings = append(warnings, "selected_staged_output_missing")
			continue
		}
		discovery.Outputs = append(discovery.Outputs, candidates[index])
	}

	listings := make([]outputListing, len(discovery.Outputs))
	parallelFor(len(discovery.Outputs), workers, func(index int) {
		listings[index] = listPayloads(discovery.Outputs[index].Path)
	})
	seen := map[string]bool{}
	for index, listing := range listings {
		warnings = append(warnings, listing.warnings...)
		for _, source := range listing.sources {
			if seen[source.key] {
				continue
			}
			seen[source.key] = true
			discovery.Sources = append(discovery.Sources, DiscoveredSource{Output: index,
				PayloadType: source.kind, Directory: source.directory, Name: source.name})
		}
	}
	if request.MaxDepth > 0 && len(discovery.Outputs) == 0 {
		warnings = append(warnings, "max_depth_may_be_too_shallow")
	}
	for _, warning := range warnings {
		if !slices.Contains(discovery.Warnings, warning) {
			discovery.Warnings = append(discovery.Warnings, warning)
		}
	}
	if len(request.TargetRoots) > 0 {
		discovery.Targets = make([]DiscoveredTarget, len(request.TargetRoots))
		// Target roots are small, so each walk is serial and the roots run
		// in parallel.
		parallelFor(len(request.TargetRoots), workers, func(index int) {
			root := request.TargetRoots[index]
			_, err := os.Stat(root)
			outputs := []string{}
			if err == nil {
				outputs = append(outputs, findTestOutputDirectories(root, request.MaxDepth, 1, true)...)
			}
			discovery.Targets[index] = DiscoveredTarget{Root: root, Exists: err == nil, Outputs: outputs}
		})
	}
	return discovery
}

// FindTestOutputDirectories is find_test_output_directories(root,
// max_depth=maxDepth, root_is_target=rootIsTarget), for the rules' doctor.
func FindTestOutputDirectories(root string, maxDepth, workers int, rootIsTarget bool) []string {
	return findTestOutputDirectories(root, maxDepth, max(1, workers), rootIsTarget)
}

// findTestOutputDirectories follows find_test_output_directories in
// topt_runtime/bep_execution.py: a sidecar or outputs.zip marks an output and
// ends the descent; otherwise a test.outputs directory, or a retry output with
// payloads, is a candidate unless it holds another. It never enters linked
// directories, but lists them, as os.walk does, and walks directories in
// parallel. A target root is never an output itself.
func findTestOutputDirectories(root string, maxDepth, workers int, rootIsTarget bool) []string {
	var mu sync.Mutex
	marked, payloadRoots, unmarked := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var pending sync.WaitGroup
	slots := make(chan struct{}, workers)
	var visit func(directory string, depth int)
	visit = func(directory string, depth int) {
		defer pending.Done()
		slots <- struct{}{}
		entries, err := os.ReadDir(directory)
		var children []string
		hasMarker, hasPayloads := false, false
		if err == nil {
			for _, entry := range entries {
				name := entry.Name()
				if isDirectory(entry, join(directory, name)) {
					hasPayloads = hasPayloads || name == "payloads"
					// os.walk lists a linked directory but does not enter it.
					if entry.Type()&os.ModeSymlink == 0 {
						children = append(children, join(directory, name))
					}
				} else if name == metadataName || name == "outputs.zip" {
					hasMarker = true
				}
			}
		}
		<-slots
		if err != nil {
			return
		}
		name := filepath.Base(directory)
		retryRoot := filepath.Base(filepath.Dir(directory)) == "test_attempts" && retryOutputDirectory.MatchString(name) &&
			(hasMarker || hasPayloads)
		if (directory != root || !rootIsTarget) && (name == "test.outputs" || retryRoot) {
			mu.Lock()
			switch {
			case hasMarker:
				marked[directory] = true
			case hasPayloads:
				payloadRoots[directory] = true
			default:
				unmarked[directory] = true
			}
			mu.Unlock()
			if hasMarker {
				return
			}
		}
		if maxDepth > 0 && depth >= maxDepth {
			return
		}
		for _, child := range children {
			pending.Add(1)
			go visit(child, depth+1)
		}
	}
	pending.Add(1)
	go visit(root, 0)
	pending.Wait()

	// Prefer actual output roots to similarly named package or target
	// ancestors, as the Python does.
	parents := map[string]bool{}
	for path := range marked {
		addAncestors(parents, path)
	}
	for path := range payloadRoots {
		addAncestors(parents, path)
	}
	selected := map[string]bool{}
	for path := range marked {
		selected[path] = true
	}
	for path := range payloadRoots {
		if !parents[path] {
			selected[path] = true
		}
	}
	remaining := map[string]bool{}
	for path := range unmarked {
		if !parents[path] && !hasAncestorIn(selected, path) {
			remaining[path] = true
		}
	}
	unmarkedParents := map[string]bool{}
	for path := range remaining {
		addAncestors(unmarkedParents, path)
	}
	var outputs []string
	for path := range selected {
		outputs = append(outputs, path)
	}
	for path := range remaining {
		if !unmarkedParents[path] {
			outputs = append(outputs, path)
		}
	}
	slices.SortFunc(outputs, func(a, b string) int { return strings.Compare(filepath.ToSlash(a), filepath.ToSlash(b)) })
	return outputs
}

// isDirectory is os.walk's test: a directory, or a link to one.
func isDirectory(entry os.DirEntry, path string) bool {
	if entry.IsDir() {
		return true
	}
	if entry.Type()&(os.ModeSymlink|os.ModeIrregular) == 0 {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// ancestors lists a path's parents, as Path.parents does.
func ancestors(path string) []string {
	var result []string
	for parent := filepath.Dir(path); parent != path; path, parent = parent, filepath.Dir(parent) {
		result = append(result, parent)
	}
	return result
}

func addAncestors(set map[string]bool, path string) {
	for _, parent := range ancestors(path) {
		set[parent] = true
	}
}

func hasAncestorIn(set map[string]bool, path string) bool {
	return slices.ContainsFunc(ancestors(path), func(parent string) bool { return set[parent] })
}

// relativeKey is output.relative_to(root).as_posix().
func relativeKey(root, output string) string {
	if output == root {
		return "."
	}
	return filepath.ToSlash(strings.TrimPrefix(output, join(root, "")))
}

// canonicalOutputKey is canonical_output_directory_key: a retry output root
// gets the virtual test.outputs suffix of the output that it replaced.
func canonicalOutputKey(key string) string {
	parts := strings.Split(key, "/")
	if len(parts) >= 2 && parts[len(parts)-2] == "test_attempts" && retryOutputDirectory.MatchString(parts[len(parts)-1]) {
		return key + "/test.outputs"
	}
	return key
}

type listedSource struct {
	key, kind, directory, name string
}

type outputListing struct {
	sources  []listedSource
	warnings []string
}

// listPayloads lists an output's payload sources as discover_file_tasks
// does. A payload directory must be real and lie inside the output: a link or
// a Windows junction below the output is refused, and so is a directory that
// resolves elsewhere.
func listPayloads(output string) outputListing {
	var listing outputListing
	resolvedOutput := ""
	for _, payload := range payloadDirectories {
		directory := join(join(output, "payloads"), payload.name)
		if info, err := os.Stat(directory); err != nil || !info.IsDir() {
			if info, err := os.Lstat(directory); err == nil && info.Mode()&os.ModeSymlink != 0 {
				listing.warnings = append(listing.warnings, "payload_symlink_skipped")
			}
			continue
		}
		if resolvedOutput == "" {
			resolved, err := resolve(output)
			if err != nil {
				listing.warnings = append(listing.warnings, "payload_symlink_skipped")
				continue
			}
			resolvedOutput = resolved
		}
		resolvedDirectory, ok := containedDirectory(output, directory, resolvedOutput)
		if !ok {
			listing.warnings = append(listing.warnings, "payload_symlink_skipped")
			continue
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			listing.warnings = append(listing.warnings, "payload_directory_unreadable")
			continue
		}
		for _, entry := range entries {
			name := entry.Name()
			suffix := strings.ToLower(pathSuffix(name))
			payloadName := suffix == ".json" || suffix == ".msgpack"
			if entry.Type()&os.ModeSymlink != 0 {
				if payloadName {
					listing.warnings = append(listing.warnings, "payload_symlink_skipped")
				}
				continue
			}
			if !payloadName || !entry.Type().IsRegular() {
				continue
			}
			listing.sources = append(listing.sources, listedSource{key: join(resolvedDirectory, name),
				kind: payload.kind, directory: payload.name, name: name})
		}
	}
	return listing
}

// containedDirectory returns a payload directory's resolved path if no
// component below the output is a link or junction and it resolves inside the
// output's resolved path.
func containedDirectory(output, directory, resolvedOutput string) (string, bool) {
	for _, current := range []string{join(output, "payloads"), directory} {
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 {
			return "", false
		}
	}
	resolved, err := resolve(directory)
	if err != nil || !strings.HasPrefix(resolved, join(resolvedOutput, "")) {
		return "", false
	}
	return resolved, true
}

func resolve(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

// pathSuffix is PurePath.suffix: the last dot's extension, except for a name
// that starts or ends with that dot.
func pathSuffix(name string) string {
	index := strings.LastIndex(name, ".")
	if index <= 0 || index == len(name)-1 {
		return ""
	}
	return name[index:]
}

func parallelFor(count, workers int, work func(int)) {
	indexes := make(chan int)
	var group sync.WaitGroup
	for range min(workers, max(count, 1)) {
		group.Go(func() {
			for index := range indexes {
				work(index)
			}
		})
	}
	for index := range count {
		indexes <- index
	}
	close(indexes)
	group.Wait()
}
