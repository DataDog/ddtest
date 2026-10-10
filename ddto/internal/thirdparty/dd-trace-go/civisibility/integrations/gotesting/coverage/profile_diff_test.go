// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package coverage

import (
	"bufio"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/utils/filebitmap"
)

// parseCoverProfile adapts readCoverProfile to the SDK's per-file map, so
// the ported tests keep their original assertions.
func parseCoverProfile(filename string) (map[string][]coverageBlock, error) {
	profile, err := readCoverProfile(filename)
	return coverProfileByFile(profile), err
}

// getFilesCovered adapts coveredFilesBetween to the SDK's per-file maps. The
// result does not depend on the order of the files.
func getFilesCovered(testFile string, before, after map[string][]coverageBlock) []coveredFile {
	return coveredFilesBetween(testFile, coverProfileFromFiles(before), coverProfileFromFiles(after))
}

func coverProfileByFile(profile coverProfile) map[string][]coverageBlock {
	files := make(map[string][]coverageBlock)
	for _, block := range profile.blocks {
		files[block.fileName] = append(files[block.fileName], block.coverageBlock)
	}
	return files
}

func coverProfileFromFiles(files map[string][]coverageBlock) coverProfile {
	var profile coverProfile
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for name := range files {
			if !yield(name) {
				return
			}
		}
	}) {
		for _, block := range files[name] {
			profile.blocks = append(profile.blocks, coverProfileBlock{fileName: name, coverageBlock: block})
		}
	}
	return profile
}

// sdkParseCoverProfile is the SDK's parseCoverProfile at the pinned base, the
// oracle for readCoverProfile.
func sdkParseCoverProfile(filename string) (map[string][]coverageBlock, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	coverageData := make(map[string][]coverageBlock)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()

		// Skip the header
		if strings.HasPrefix(line, "mode:") {
			continue
		}

		fileName, blockInfo, err := splitCoverageProfileLine(line)
		if err != nil {
			continue
		}

		// Split the block info by space
		infoParts := strings.Fields(blockInfo)
		if len(infoParts) < 3 {
			continue
		}

		// Extract start and end positions (line.column)
		startEnd := strings.Split(infoParts[0], ",")
		if len(startEnd) < 2 {
			continue
		}

		startPos := strings.Split(startEnd[0], ".")
		endPos := strings.Split(startEnd[1], ".")

		if len(startPos) < 2 || len(endPos) < 2 {
			continue
		}

		// Convert to integers
		startLine, err1 := strconv.Atoi(startPos[0])
		startCol, err2 := strconv.Atoi(startPos[1])
		endLine, err3 := strconv.Atoi(endPos[0])
		endCol, err4 := strconv.Atoi(endPos[1])
		numStmt, err5 := strconv.Atoi(infoParts[1])
		count, err6 := strconv.Atoi(infoParts[2])

		if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil || err6 != nil {
			continue
		}

		block := coverageBlock{
			startLine: startLine,
			startCol:  startCol,
			endLine:   endLine,
			endCol:    endCol,
			numStmt:   numStmt,
			count:     count,
		}

		coverageData[fileName] = append(coverageData[fileName], block)
	}

	return coverageData, scanner.Err()
}

// sdkGetFilesCovered is the SDK's getFilesCovered at the pinned base, the
// oracle for coveredFilesBetween.
func sdkGetFilesCovered(testFile string, before, after map[string][]coverageBlock) []coveredFile {
	coveredByName := map[string]*filebitmap.FileBitmap{}

	addCoveredRange := func(fileName string, block coverageBlock) {
		name := getRelativePathFromCITagsSourceRootForCoverage(fileName)
		bitmap := coveredByName[name]
		if bitmap == nil || bitmap.BitCount() < block.endLine {
			next := filebitmap.FromLineCount(block.endLine)
			if bitmap != nil {
				next = filebitmap.Or(next, bitmap, true)
			}
			bitmap = next
			coveredByName[name] = bitmap
		}
		for line := block.startLine; line <= block.endLine; line++ {
			bitmap.Set(line)
		}
	}

	for fileName, afterBlocks := range after {
		if beforeBlocks, found := before[fileName]; found {
			// Create a map for quick lookup by (startLine, startCol, endLine, endCol)
			beforeMap := make(map[string]coverageBlock)
			for _, block := range beforeBlocks {
				key := fmt.Sprintf("%d.%d-%d.%d", block.startLine, block.startCol, block.endLine, block.endCol)
				beforeMap[key] = block
			}

			// Subtract each block in after from the corresponding block in before
			for _, afterBlock := range afterBlocks {
				key := fmt.Sprintf("%d.%d-%d.%d", afterBlock.startLine, afterBlock.startCol, afterBlock.endLine, afterBlock.endCol)
				if beforeBlock, found := beforeMap[key]; found {
					// Subtract hit counts
					diffCount := afterBlock.count - beforeBlock.count
					if diffCount > 0 {
						addCoveredRange(fileName, afterBlock)
					}
				} else if afterBlock.count > 0 {
					// If there's no matching block in before, add the whole block from after
					addCoveredRange(fileName, afterBlock)
				}
			}
		} else {
			// If there's no before profile for this file, add the entire after profile
			for _, afterBlock := range afterBlocks {
				if afterBlock.count > 0 {
					addCoveredRange(fileName, afterBlock)
				}
			}
		}
	}

	names := make([]string, 0, len(coveredByName))
	for name := range coveredByName {
		names = append(names, name)
	}
	slices.Sort(names)
	result := make([]coveredFile, 0, 1+len(names))
	result = append(result, coveredFile{name: testFile})
	for _, name := range names {
		result = append(result, coveredFile{name: name, bitmap: coveredByName[name].ToArray()})
	}
	return result
}

// withCoverageModule maps modulePath to moduleDir for relative names, without
// starting go list, and restores the previous module afterwards.
func withCoverageModule(t testing.TB, path, dir string) {
	t.Helper()
	previousPath, previousDir := modulePath, moduleDir
	moduleInfoSource.Lock()
	previousPending := moduleInfoSource.pending
	moduleInfoSource.pending = false
	moduleInfoSource.Unlock()
	modulePath, moduleDir = path, dir
	t.Cleanup(func() {
		modulePath, moduleDir = previousPath, previousDir
		moduleInfoSource.Lock()
		moduleInfoSource.pending = previousPending
		moduleInfoSource.Unlock()
	})
}

func writeCoverProfile(t testing.TB, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profile.out")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func requireSameParse(t *testing.T, text string) {
	t.Helper()
	path := writeCoverProfile(t, text)
	want, wantErr := sdkParseCoverProfile(path)
	profile, gotErr := readCoverProfile(path)
	if (wantErr == nil) != (gotErr == nil) {
		t.Fatalf("error mismatch: sdk %v, ordered %v\n%q", wantErr, gotErr, text)
	}
	if got := coverProfileByFile(profile); !reflect.DeepEqual(got, want) {
		t.Fatalf("parse mismatch for %q:\nsdk     %v\nordered %v", text, want, got)
	}
}

// The parser accepts and skips exactly the lines the SDK's string-splitting
// parser did, including signs, extra fields, Unicode whitespace and invalid
// UTF-8.
func TestReadCoverProfileMatchesSDKParser(t *testing.T) {
	for _, text := range []string{
		"",
		"mode: count\n",
		"mode: set\na/b.go:1.2,3.4 5 6\n",
		"a/b.go:1.2,3.4 5 6\r\na/b.go:7.1,8.2 1 0\r\n",
		"a/b.go:1.2,3.4 5 6",
		"a/b.go:+1.-2,3.4 +5 -6\n",
		"a/b.go:1.2,3.4 5 6 extra fields\n",
		"a/b.go:1.2.9,3.4.9,5.6 5 6\n",
		"a/b.go:1.2,3.4 5\n",
		"a/b.go:1,3.4 5 6\n",
		"a/b.go:1.2 5 6\n",
		"a/b.go:1.2,3.x 5 6\n",
		"a/b.go:1.2,3.4 5 6x\n",
		"a/b.go:1.2,3.4 5 99999999999999999999\n",
		"a/b.go:1.2,3.4 5 9223372036854775807\n",
		"a/b.go:1.2,3.4 5 -9223372036854775808\n",
		"a/b.go:1.2,3.4 5 0000000000000000000001\n",
		"a/b.go:-.2,3.4 5 6\n",
		"a/b.go:1.2,3.4\t\v\f5   6\n",
		"a/b.go:1.2,3.4\u00a05\u00856\n",
		"a/b.go:1.2,3.4\u20285 6\n",
		"a/b.go:1.2,3.4 \xff5 6\n",
		"a/b.go:1.2,3.4 5 6\xff\n",
		"a/b.go:１.2,3.4 5 6\n",
		"   :1.2,3.4 5 6\n",
		"\u00a0:1.2,3.4 5 6\n",
		":1.2,3.4 5 6\n",
		"no separator 1.2,3.4 5 6\n",
		`C:\work\pkg\b.go:1.2,3.4 5 6` + "\n",
		"a/b.go:c.go:1.2,3.4 5 6\n",
		"  mode: count\nmode:x\na b.go:1.2,3.4 5 6\n",
		"a.go:1.2,3.4 5 6\nb.go:1.2,3.4 5 6\na.go:9.2,9.4 5 6\n",
		strings.Repeat("x", bufio.MaxScanTokenSize+1) + ":1.2,3.4 5 6\n",
	} {
		requireSameParse(t, text)
	}

	rng := rand.New(rand.NewPCG(1, 2))
	pieces := []string{"a.go", "b/c.go", ":", ",", ".", " ", "\t", "\r", "-", "+", "0", "1", "42", "x", "\u00a0", "\xff", "mode:"}
	for range 2000 {
		var builder strings.Builder
		for range 1 + rng.IntN(6) {
			for range rng.IntN(16) {
				builder.WriteString(pieces[rng.IntN(len(pieces))])
			}
			if rng.IntN(3) == 0 {
				fmt.Fprintf(&builder, "f%d.go:%d.%d,%d.%d %d %d", rng.IntN(3), rng.IntN(9)-1, rng.IntN(9), rng.IntN(9)-1, rng.IntN(9), rng.IntN(3), rng.IntN(3)-1)
			}
			builder.WriteByte('\n')
		}
		requireSameParse(t, builder.String())
	}
}

// generateTestingProfiles returns a before and after profile laid out as
// testing writes them: each file in one run, positions sorted and unique.
func generateTestingProfiles(rng *rand.Rand, files, blocksPerFile int) ([]coverProfileBlock, []coverProfileBlock) {
	var before, after []coverProfileBlock
	for file := range files {
		name := fmt.Sprintf("example.com/m/pkg%d/file%d.go", file%3, file)
		if file%4 == 3 {
			// Resolves to the same relative name as the previous file.
			name = fmt.Sprintf("/w/pkg%d/file%d.go", (file-1)%3, file-1)
		}
		keys := make(map[coverageBlockKey]struct{})
		line := 1
		for range blocksPerFile {
			line += rng.IntN(4)
			key := coverageBlockKey{startLine: line, startCol: 1 + rng.IntN(3), endLine: line + rng.IntN(5), endCol: 2 + rng.IntN(20)}
			if rng.IntN(5) == 0 {
				// Single-line blocks that differ only by column.
				key.endLine, key.startCol = key.startLine, 30+rng.IntN(3)*5
			}
			keys[key] = struct{}{}
		}
		sorted := slices.SortedFunc(func(yield func(coverageBlockKey) bool) {
			for key := range keys {
				if !yield(key) {
					return
				}
			}
		}, func(a, b coverageBlockKey) int {
			if a.before(b) {
				return -1
			}
			if b.before(a) {
				return 1
			}
			return 0
		})
		for _, key := range sorted {
			block := coverageBlock{startLine: key.startLine, startCol: key.startCol, endLine: key.endLine, endCol: key.endCol, numStmt: rng.IntN(4)}
			block.count = rng.IntN(3)
			before = append(before, coverProfileBlock{fileName: name, coverageBlock: block})
			block.count += rng.IntN(3) - 1
			after = append(after, coverProfileBlock{fileName: name, coverageBlock: block})
		}
	}
	return before, after
}

// adjacentBlocksShareFile reports whether some file has more than one block.
func adjacentBlocksShareFile(blocks []coverProfileBlock) bool {
	for i := 1; i < len(blocks); i++ {
		if blocks[i-1].fileName == blocks[i].fileName {
			return true
		}
	}
	return false
}

func profileText(blocks []coverProfileBlock) string {
	var builder strings.Builder
	builder.WriteString("mode: count\n")
	for _, block := range blocks {
		fmt.Fprintf(&builder, "%s:%d.%d,%d.%d %d %d\n", block.fileName, block.startLine, block.startCol, block.endLine, block.endCol, block.numStmt, block.count)
	}
	return builder.String()
}

func requireSameCoveredFiles(t *testing.T, beforeText, afterText string, wantSameLayout bool) {
	t.Helper()
	beforePath, afterPath := writeCoverProfile(t, beforeText), writeCoverProfile(t, afterText)
	sdkBefore, err := sdkParseCoverProfile(beforePath)
	if err != nil {
		t.Fatal(err)
	}
	sdkAfter, err := sdkParseCoverProfile(afterPath)
	if err != nil {
		t.Fatal(err)
	}
	before, err := readCoverProfile(beforePath)
	if err != nil {
		t.Fatal(err)
	}
	after, err := readCoverProfile(afterPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := sameCoverProfileLayout(before, after); got != wantSameLayout {
		t.Fatalf("sameCoverProfileLayout = %t, want %t", got, wantSameLayout)
	}
	want := sdkGetFilesCovered("pkg/lib_test.go", sdkBefore, sdkAfter)
	got := coveredFilesBetween("pkg/lib_test.go", before, after)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("covered files differ:\nsdk     %v\nordered %v\nbefore:\n%s\nafter:\n%s", want, got, beforeText, afterText)
	}
}

// The ordered comparison returns the SDK's covered files and bitmaps: the test
// file first, then sorted names, for profiles with testing's layout (compared
// by index) and for every other shape (compared by key).
func TestCoveredFilesMatchesSDK(t *testing.T) {
	withCoverageModule(t, "example.com/m", "/w")
	rng := rand.New(rand.NewPCG(3, 4))
	for iteration := range 300 {
		before, after := generateTestingProfiles(rng, 1+rng.IntN(6), 1+rng.IntN(40))
		requireSameCoveredFiles(t, profileText(before), profileText(after), true)

		switch iteration % 6 {
		case 0: // Files in another order.
			rotated := append(slices.Clone(after[len(after)/2:]), after[:len(after)/2]...)
			requireSameCoveredFiles(t, profileText(before), profileText(rotated), len(after) < 2)
		case 1: // A file only in the after profile.
			extra := append(slices.Clone(after), coverProfileBlock{fileName: "example.com/m/new.go", coverageBlock: coverageBlock{startLine: 3, startCol: 1, endLine: 9, endCol: 2, numStmt: 1, count: 1}})
			requireSameCoveredFiles(t, profileText(before), profileText(extra), false)
		case 2: // A file only in the before profile.
			extra := append(slices.Clone(before), coverProfileBlock{fileName: "example.com/m/old.go", coverageBlock: coverageBlock{startLine: 1, startCol: 1, endLine: 2, endCol: 2, count: 5}})
			requireSameCoveredFiles(t, profileText(extra), profileText(after), false)
		case 3: // Duplicate positions with different counts: the last before block wins.
			duplicated := append(slices.Clone(before), before[len(before)-1])
			duplicated[len(duplicated)-1].count += 1 + rng.IntN(2)
			afterDuplicated := append(slices.Clone(after), after[len(after)-1])
			afterDuplicated[len(afterDuplicated)-1].count = rng.IntN(4)
			requireSameCoveredFiles(t, profileText(duplicated), profileText(afterDuplicated), false)
		case 4: // A changed position.
			changed := slices.Clone(after)
			changed[rng.IntN(len(changed))].endCol += 100
			requireSameCoveredFiles(t, profileText(before), profileText(changed), false)
		case 5: // Blocks out of testing's order.
			reversed := slices.Clone(after)
			slices.Reverse(reversed)
			reversedBefore := slices.Clone(before)
			slices.Reverse(reversedBefore)
			requireSameCoveredFiles(t, profileText(reversedBefore), profileText(reversed), !adjacentBlocksShareFile(reversed))
		}
	}
}

// Edge cases from the SDK's tests and lines with odd ranges: zero-statement
// blocks, an empty range, an end line below 1 and merged relative names.
func TestCoveredFilesEdgeCasesMatchSDK(t *testing.T) {
	withCoverageModule(t, "example.com/m", "/w")
	for _, test := range []struct {
		before, after string
		sameLayout    bool
	}{
		{"", "", true},
		{"a.go:1.1,1.9 1 0\n", "a.go:1.1,1.9 1 0\n", true},
		{"a.go:23.19,24.2 0 0\n", "a.go:23.19,24.2 0 1\n", true},
		{"a.go:9.1,4.2 1 0\n", "a.go:9.1,4.2 1 3\n", true},
		{"a.go:9.1,0.2 1 0\n", "a.go:9.1,0.2 1 3\n", true},
		{"a.go:9.1,-14.2 1 0\n", "a.go:9.1,-14.2 1 3\n", true},
		{"a.go:1.1,1.9 1 2\na.go:2.1,2.9 1 3\n", "a.go:1.1,1.9 1 2\na.go:2.1,2.9 1 1\n", true},
		{"", "a.go:1.1,1.9 1 1\na.go:8.1,8.9 1 1\n", false},
		{"example.com/m/a.go:1.1,2.1 1 0\n/w/a.go:5.1,6.1 1 0\n", "example.com/m/a.go:1.1,2.1 1 1\n/w/a.go:5.1,6.1 1 1\n", true},
		{"a.go:2.1,2.9 1 0\na.go:1.1,1.9 1 0\n", "a.go:2.1,2.9 1 1\na.go:1.1,1.9 1 1\n", false},
		{"a.go:1.1,1.9 1 0\nb.go:1.1,1.9 1 0\na.go:2.1,2.9 1 0\n", "a.go:1.1,1.9 1 1\nb.go:1.1,1.9 1 1\na.go:2.1,2.9 1 1\n", false},
		{"a.go:1.1,1.9 1 0\na.go:1.1,1.9 1 4\n", "a.go:1.1,1.9 1 1\na.go:1.1,1.9 1 5\n", false},
		{"a.go:1.1,1.9 1 0\n", "a.go:1.1,1.9 1 1 \n", true},
	} {
		requireSameCoveredFiles(t, "mode: count\n"+test.before, "mode: count\n"+test.after, test.sameLayout)
	}
}

// The SDK panicked when a covered block started below line 1. The ordered
// comparison sets the block's lines from 1 instead.
func TestCoveredFilesSkipsLinesBelowOne(t *testing.T) {
	withCoverageModule(t, "", "")
	before := coverProfile{blocks: []coverProfileBlock{{fileName: "a.go", coverageBlock: coverageBlock{startLine: 0, endLine: 3}}}}
	after := coverProfile{blocks: []coverProfileBlock{{fileName: "a.go", coverageBlock: coverageBlock{startLine: 0, endLine: 3, count: 1}}}}
	got := coveredFilesBetween("a_test.go", before, after)
	want := filebitmap.FromActiveRange(1, 3).ToArray()
	if len(got) != 2 || got[1].name != "a.go" || !slices.Equal(got[1].bitmap, want) {
		t.Fatalf("got %#v", got)
	}
}

// BenchmarkCoverageProfileDiff parses a before and after profile of 5,000
// blocks, half of them covered, and subtracts them, with the SDK's algorithm
// and the ordered one.
func BenchmarkCoverageProfileDiff(b *testing.B) {
	withCoverageModule(b, "example.com/m", "/w")
	var before, after strings.Builder
	before.WriteString("mode: count\n")
	after.WriteString("mode: count\n")
	for i := range 5000 {
		line := (i%100)*5 + 1
		block := fmt.Sprintf("example.com/m/pkg/file%02d.go:%d.2,%d.16 2", i/100, line, line+3)
		fmt.Fprintf(&before, "%s 0\n", block)
		fmt.Fprintf(&after, "%s %d\n", block, i%2)
	}
	beforePath, afterPath := writeCoverProfile(b, before.String()), writeCoverProfile(b, after.String())
	b.Run("sdk", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			previous, _ := sdkParseCoverProfile(beforePath)
			current, _ := sdkParseCoverProfile(afterPath)
			sdkGetFilesCovered("pkg/x_test.go", previous, current)
		}
	})
	b.Run("ordered", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			previous, _ := readCoverProfile(beforePath)
			current, _ := readCoverProfile(afterPath)
			coveredFilesBetween("pkg/x_test.go", previous, current)
		}
	})
}
