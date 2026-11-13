package planner

import (
	"container/heap"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
)

// DistributeTestFiles distributes test files using weights loaded into this planner.
func (tp *TestPlanner) DistributeTestFiles(testFiles []string, parallelRunners int) [][]string {
	if !tp.planLoaded {
		if err := tp.restoreTestOptimizationPlanCache(); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				slog.Debug("Test optimization run artifacts not found; distributing test files with default weights")
			} else {
				slog.Warn("Failed to load test optimization run artifacts; distributing test files with default weights", "error", err)
			}
		}
	}

	testFileWeights := testFileWeightsForFiles(tp.testFileWeights, testFiles)
	return tp.DistributeWeightedTestFiles(testFileWeights, parallelRunners)
}

// DistributeWeightedTestFiles distributes test files across parallel runners using weighted list scheduling.
// For Python projects, files that pytest would import under the same module
// name are kept on distinct runners when possible: two such files collected in
// one worker fail with an import file mismatch (see pytestModuleName).
func (tp *TestPlanner) DistributeWeightedTestFiles(testFiles map[string]int, parallelRunners int) [][]string {
	builder := newTestSplitBuilder(parallelRunners)
	return builder.distributeFiles(testFiles, tp.basenameAffinityEnabled())
}

// basenameAffinityEnabled reports whether files sharing a basename must be
// kept on distinct runners (see DistributeWeightedTestFiles). Platform names
// are plain strings (see platform.Python.Name).
func (tp *TestPlanner) basenameAffinityEnabled() bool {
	return tp.planMetadata.Platform == "python"
}

// pytestModuleName is the module name pytest's default import mode (prepend)
// gives a test file: its basename without the extension when its directory
// isn't a package, and otherwise its dotted path from the directory above its
// outermost package, the closest ancestor directories that have an
// __init__.py. Two files only collide in one pytest process when their module
// names are equal, so test files in packages, as in most large projects, don't
// collide however many share a basename. Package lookups are cached per
// directory, since many files share each one.
type pytestModuleNamer struct {
	isPackage map[string]bool
}

func newPytestModuleNamer() *pytestModuleNamer {
	return &pytestModuleNamer{isPackage: make(map[string]bool)}
}

func (n *pytestModuleNamer) pytestModuleName(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	for directory := filepath.Dir(path); n.directoryIsPackage(directory); directory = filepath.Dir(directory) {
		name = filepath.Base(directory) + "." + name
		if parent := filepath.Dir(directory); parent == directory {
			break
		}
	}
	return name
}

func (n *pytestModuleNamer) directoryIsPackage(directory string) bool {
	if directory == "." || directory == string(filepath.Separator) || filepath.Dir(directory) == directory {
		return false
	}
	if isPackage, ok := n.isPackage[directory]; ok {
		return isPackage
	}
	_, err := os.Stat(filepath.Join(directory, "__init__.py"))
	n.isPackage[directory] = err == nil
	return err == nil
}

// hasDuplicateBasenames reports whether any two files would get the same
// pytest module name (see pytestModuleName).
func hasDuplicateBasenames(testFiles map[string]int) bool {
	namer := newPytestModuleNamer()
	basenames := make(map[string]int, len(testFiles))
	for path := range testFiles {
		basenames[namer.pytestModuleName(path)]++
	}
	for _, count := range basenames {
		if count > 1 {
			return true
		}
	}
	return false
}

// basenameCollisionGroups groups files that share a pytest module name (see
// pytestModuleName) with at least one other file. Groups are ordered by total
// weight (desc) and each group's members by weight (desc) for deterministic,
// reasonably balanced placement.
func basenameCollisionGroups(testFiles map[string]int) [][]weightedTestFile {
	namer := newPytestModuleNamer()
	byBasename := make(map[string][]weightedTestFile, len(testFiles))
	for path, weight := range testFiles {
		name := namer.pytestModuleName(path)
		byBasename[name] = append(byBasename[name], weightedTestFile{path: path, weight: weight})
	}
	groups := make([][]weightedTestFile, 0)
	for _, files := range byBasename {
		if len(files) > 1 {
			sortWeightedFilesDesc(files)
			groups = append(groups, files)
		}
	}
	slices.SortFunc(groups, func(a, b []weightedTestFile) int {
		weightA, weightB := 0, 0
		for _, f := range a {
			weightA += f.weight
		}
		for _, f := range b {
			weightB += f.weight
		}
		if weightA != weightB {
			if weightA > weightB {
				return -1
			}
			return 1
		}
		if a[0].path < b[0].path {
			return -1
		}
		if a[0].path > b[0].path {
			return 1
		}
		return 0
	})
	return groups
}

func sortWeightedFilesDesc(files []weightedTestFile) {
	slices.SortFunc(files, func(a, b weightedTestFile) int {
		if a.weight != b.weight {
			if a.weight > b.weight {
				return -1
			}
			return 1
		}
		if a.path < b.path {
			return -1
		}
		if a.path > b.path {
			return 1
		}
		return 0
	})
}

func testFileWeightsForFiles(cacheWeights map[string]int, testFiles []string) map[string]int {
	testFileWeights := make(map[string]int, len(testFiles))
	for _, testFile := range testFiles {
		if cachedWeight, ok := cacheWeights[testFile]; ok && cachedWeight > 0 {
			testFileWeights[testFile] = cachedWeight
		} else {
			testFileWeights[testFile] = constants.DefaultTestFileWeight
		}
	}
	return testFileWeights
}

// CreateTestSplits creates test split files for parallel runners
// For multiple runners: distributes files using weighted list scheduling and writes to separate runner files
// For single runner: copies test-files.txt content to runner-0
func (tp *TestPlanner) CreateTestSplits(testFiles map[string]int, parallelRunners int, testFilesOutputPath string) error {
	if tp.planMetadata.Platform == "python" && parallelRunners <= 1 && hasDuplicateBasenames(testFiles) {
		// Single-runner distribution copies files without separating them, so
		// pytest same-basename import collisions cannot be avoided here.
		for _, group := range basenameCollisionGroups(testFiles) {
			files := make([]string, 0, len(group))
			for _, file := range group {
				files = append(files, file.path)
			}
			slog.Warn("Test files with the same pytest module name share a single runner; pytest import collisions cannot be avoided",
				"basename", filepath.Base(files[0]), "files", files,
				"hint", "use more parallel runners, add __init__.py packages, or rename files")
		}
	}
	if parallelRunners > 1 {
		// Distribute test files across parallel runners using weighted list scheduling.
		distribution := expandSplitUnits(tp.DistributeWeightedTestFiles(testFiles, parallelRunners), tp.splitLines)
		if err := writeDistributedTestSplits(distribution, constants.TestsSplitDir); err != nil {
			return err
		}
		if err := writeSplitFileTests(tp.splitListed); err != nil {
			return err
		}
	} else {
		// The single runner gets test-files.txt, whose files are whole.
		if err := writeSplitFileTests(nil); err != nil {
			return err
		}
		// For single runner, copy test-files.txt to runner-0
		testFilesData, err := os.ReadFile(testFilesOutputPath)
		if err != nil {
			return fmt.Errorf("failed to read test files from %s: %w", testFilesOutputPath, err)
		}

		if err := writeRunnerSplit(constants.TestsSplitDir, 0, testFilesData); err != nil {
			return err
		}
	}

	return nil
}

type weightedTestFile struct {
	path   string
	weight int
}

func sortedWeightedTestFiles(testFiles map[string]int) []weightedTestFile {
	files := make([]weightedTestFile, 0, len(testFiles))
	for path, weight := range testFiles {
		files = append(files, weightedTestFile{path: path, weight: weight})
	}

	slices.SortFunc(files, func(a, b weightedTestFile) int {
		if a.weight > b.weight {
			return -1
		}
		if a.weight < b.weight {
			return 1
		}
		if a.path < b.path {
			return -1
		}
		if a.path > b.path {
			return 1
		}
		return 0
	})

	return files
}

type splitScore struct {
	parallelRunners int
	wallTime        int
	imbalance       int
	totalRuntime    int
}

func (s splitScore) wallTimeDuration() time.Duration {
	return time.Duration(s.wallTime) * time.Millisecond
}

func (s splitScore) imbalanceDuration() time.Duration {
	return time.Duration(s.imbalance) * time.Millisecond
}

func (s splitScore) totalRuntimeDuration() time.Duration {
	return time.Duration(s.totalRuntime) * time.Millisecond
}

// preparedSplitFiles precomputes the file ordering and same-basename
// collision groups that split placement needs. Preparing once lets runner
// selection score every candidate without re-sorting or regrouping the files.
type preparedSplitFiles struct {
	sortedFiles      []weightedTestFile
	collisionGroups  [][]int // sortedFiles indexes sharing a basename, in placement order
	inCollisionGroup []bool  // whether each sortedFiles entry belongs to a collision group
}

// prepareSplitFiles orders files for weighted scheduling and, when basename
// affinity applies, resolves same-basename collision groups to sortedFiles
// indexes.
func prepareSplitFiles(testFiles map[string]int, basenameAffinity bool) preparedSplitFiles {
	prepared := preparedSplitFiles{sortedFiles: sortedWeightedTestFiles(testFiles)}
	// Allocated unconditionally: the placement loops index it even when
	// no collision groups exist.
	prepared.inCollisionGroup = make([]bool, len(prepared.sortedFiles))
	if !basenameAffinity || !hasDuplicateBasenames(testFiles) {
		return prepared
	}
	indexByPath := make(map[string]int, len(prepared.sortedFiles))
	for i, file := range prepared.sortedFiles {
		indexByPath[file.path] = i
	}
	prepared.inCollisionGroup = make([]bool, len(prepared.sortedFiles))
	for _, group := range basenameCollisionGroups(testFiles) {
		indexes := make([]int, len(group))
		for i, file := range group {
			indexes[i] = indexByPath[file.path]
			prepared.inCollisionGroup[indexes[i]] = true
		}
		prepared.collisionGroups = append(prepared.collisionGroups, indexes)
	}
	return prepared
}

// scoreRunnerSplit scores a candidate runner count by simulating the exact
// placement used to create test splits, including Python basename affinity
// when enabled. Scoring must match split creation, or the selected runner
// count could report a target-meeting plan whose generated splits miss the
// target.
func scoreRunnerSplit(testFiles map[string]int, parallelRunners int, basenameAffinity bool) splitScore {
	return scorePreparedRunnerSplit(prepareSplitFiles(testFiles, basenameAffinity), parallelRunners)
}

// scorePreparedRunnerSplit scores a candidate against already prepared files
// without building runner file lists, so wide parallelism ranges stay cheap.
func scorePreparedRunnerSplit(prepared preparedSplitFiles, parallelRunners int) splitScore {
	return scorePreparedNodeSplit(prepared, parallelRunners, defaultNodeModel)
}

func scorePreparedNodeSplit(prepared preparedSplitFiles, parallelRunners int, model nodeModel) splitScore {
	builder := newTestSplitBuilder(parallelRunners)
	builder.placePreparedFiles(prepared, nil)
	return builder.scoreNodes(model)
}

// nodeModel is how long a CI node takes for the files it gets. Each file's
// weight is its duration in one test process; a node runs its load speedup
// times faster, as when it runs its files with several processes such as
// pytest-xdist workers, and pays fileOverhead for every file, collecting it.
// The speedup is what the node really gets, not its process count: two
// pytest-xdist workers on two vCPUs ran sympy's tests about 1.5 times as fast
// as one process, and four workers on four vCPUs about 2.6 times.
type nodeModel struct {
	speedup      float64
	fileOverhead time.Duration
}

// defaultNodeModel is one test process per node and no cost per file.
var defaultNodeModel = nodeModel{speedup: 1}

// nodeTime returns the modeled milliseconds of a node with load milliseconds
// of tests in files files.
func (m nodeModel) nodeTime(load, files int) int {
	speedup := max(m.speedup, 1)
	return int(math.Ceil(float64(load)/speedup)) + files*int(m.fileOverhead/time.Millisecond)
}

type testSplitBuilder struct {
	parallelRunners int
	loads           minLoadHeap
}

func newTestSplitBuilder(parallelRunners int) testSplitBuilder {
	if parallelRunners <= 0 {
		parallelRunners = 1
	}

	return testSplitBuilder{
		parallelRunners: parallelRunners,
		loads:           makeMinLoadHeap(parallelRunners),
	}
}

func (b *testSplitBuilder) addFile(weight int) int {
	lightestRunner := heap.Pop(&b.loads).(runnerLoad)
	lightestRunner.load += weight
	lightestRunner.files++
	heap.Push(&b.loads, lightestRunner)
	return lightestRunner.index
}

func (b *testSplitBuilder) distributeFiles(testFiles map[string]int, basenameAffinity bool) [][]string {
	result, unavoidableCollisions := b.placeFiles(testFiles, basenameAffinity)
	for basename, files := range unavoidableCollisions {
		slog.Warn("Test files with the same pytest module name exceed the runner count; pytest import collisions cannot be avoided",
			"basename", basename, "files", files, "parallelRunners", b.parallelRunners)
	}
	return result
}

// placeFiles distributes files across runners without logging, so candidate
// scoring can reuse the exact split-creation placement.
func (b *testSplitBuilder) placeFiles(testFiles map[string]int, basenameAffinity bool) ([][]string, map[string][]string) {
	return b.placePrepared(prepareSplitFiles(testFiles, basenameAffinity))
}

func (b *testSplitBuilder) placePrepared(prepared preparedSplitFiles) ([][]string, map[string][]string) {
	assignments := make([]int, len(prepared.sortedFiles))
	collisions := b.placePreparedFiles(prepared, assignments)
	// Rebuild the runner lists in placement order: same-basename groups
	// first, then the remaining files by descending weight.
	result := make([][]string, b.parallelRunners)
	for i := range result {
		result[i] = []string{}
	}
	for _, group := range prepared.collisionGroups {
		for _, fileIndex := range group {
			runnerIndex := assignments[fileIndex]
			result[runnerIndex] = append(result[runnerIndex], prepared.sortedFiles[fileIndex].path)
		}
	}
	for i, file := range prepared.sortedFiles {
		if prepared.inCollisionGroup[i] {
			continue
		}
		result[assignments[i]] = append(result[assignments[i]], file.path)
	}
	return result, collisions
}

// placePreparedFiles applies the split-creation placement to the builder's
// runner loads. When assignments is non-nil, the chosen runner index is
// recorded for every sortedFiles entry; load-only scoring passes nil so no
// output lists are constructed. It returns same-basename groups that could
// not be placed on distinct runners.
func (b *testSplitBuilder) placePreparedFiles(prepared preparedSplitFiles, assignments []int) map[string][]string {
	var collisions map[string][]string
	for _, group := range prepared.collisionGroups {
		usedRunners := make(map[int]bool, len(group))
		for _, fileIndex := range group {
			weight := prepared.sortedFiles[fileIndex].weight
			runnerIndex, ok := b.lightestUnusedRunner(usedRunners)
			if !ok {
				// More same-basename files than runners: the collision cannot
				// be avoided; place the rest for balance and report it.
				if collisions == nil {
					collisions = make(map[string][]string)
				}
				basename := filepath.Base(prepared.sortedFiles[fileIndex].path)
				collisions[basename] = append(collisions[basename], prepared.sortedFiles[fileIndex].path)
				runnerIndex = b.addFile(weight)
			} else {
				b.addLoadToRunner(runnerIndex, weight)
			}
			usedRunners[runnerIndex] = true
			if assignments != nil {
				assignments[fileIndex] = runnerIndex
			}
		}
	}
	for i, file := range prepared.sortedFiles {
		if prepared.inCollisionGroup[i] {
			continue
		}
		runnerIndex := b.addFile(file.weight)
		if assignments != nil {
			assignments[i] = runnerIndex
		}
	}
	return collisions
}

// lightestUnusedRunner returns the least-loaded runner whose index is not in
// used, preferring lower runner indexes on ties.
func (b *testSplitBuilder) lightestUnusedRunner(used map[int]bool) (int, bool) {
	best := -1
	bestLoad := 0
	for _, load := range b.loads {
		if used[load.index] {
			continue
		}
		if best == -1 || load.load < bestLoad || (load.load == bestLoad && load.index < best) {
			best = load.index
			bestLoad = load.load
		}
	}
	return best, best != -1
}

// addLoadToRunner adds a file of weight to a runner by index, keeping the
// heap valid.
func (b *testSplitBuilder) addLoadToRunner(index int, weight int) {
	for i := range b.loads {
		if b.loads[i].index == index {
			b.loads[i].load += weight
			b.loads[i].files++
			heap.Fix(&b.loads, i)
			return
		}
	}
}

func (b testSplitBuilder) score() splitScore {
	return b.scoreNodes(defaultNodeModel)
}

// scoreNodes times each runner with model: the wall time and imbalance are
// in modeled node time, the total runtime is the files' weights.
func (b testSplitBuilder) scoreNodes(model nodeModel) splitScore {
	_, _, totalLoad := loadStats(b.loads)
	minTime, maxTime := -1, 0
	for _, load := range b.loads {
		t := model.nodeTime(load.load, load.files)
		if minTime == -1 || t < minTime {
			minTime = t
		}
		maxTime = max(maxTime, t)
	}
	return splitScore{
		parallelRunners: b.parallelRunners,
		wallTime:        maxTime,
		imbalance:       maxTime - minTime,
		totalRuntime:    totalLoad,
	}
}

type runnerLoad struct {
	index int
	load  int
	files int
}

type minLoadHeap []runnerLoad

func (h minLoadHeap) Len() int {
	return len(h)
}

func (h minLoadHeap) Less(i, j int) bool {
	if h[i].load == h[j].load {
		return h[i].index < h[j].index
	}
	return h[i].load < h[j].load
}

func (h minLoadHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *minLoadHeap) Push(x any) {
	*h = append(*h, x.(runnerLoad))
}

func (h *minLoadHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

func makeMinLoadHeap(parallelRunners int) minLoadHeap {
	loads := make(minLoadHeap, parallelRunners)
	for i := range loads {
		loads[i] = runnerLoad{index: i}
	}
	heap.Init(&loads)
	return loads
}

func loadStats(loads []runnerLoad) (int, int, int) {
	minLoad := loads[0].load
	maxLoad := loads[0].load
	totalLoad := loads[0].load
	for _, load := range loads[1:] {
		if load.load < minLoad {
			minLoad = load.load
		}
		if load.load > maxLoad {
			maxLoad = load.load
		}
		totalLoad += load.load
	}
	return minLoad, maxLoad, totalLoad
}

func writeDistributedTestSplits(distribution [][]string, testsSplitDir string) error {
	for i, runnerFiles := range distribution {
		runnerContent := strings.Join(runnerFiles, "\n")
		if len(runnerFiles) > 0 {
			runnerContent += "\n"
		}

		if err := writeRunnerSplit(testsSplitDir, i, []byte(runnerContent)); err != nil {
			return err
		}
	}

	return nil
}

func writeRunnerSplit(testsSplitDir string, runnerIndex int, content []byte) error {
	if err := os.MkdirAll(testsSplitDir, 0755); err != nil {
		return fmt.Errorf("failed to create tests-split directory %s: %w", testsSplitDir, err)
	}

	runnerFilePath := filepath.Join(testsSplitDir, fmt.Sprintf("runner-%d", runnerIndex))
	if err := os.WriteFile(runnerFilePath, content, 0644); err != nil {
		return fmt.Errorf("failed to write runner-%d files to %s: %w", runnerIndex, runnerFilePath, err)
	}

	return nil
}
