// Command vendor-msgpack refreshes the pinned MessagePack runtime sources.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tonyredondo/dd-ci-testing-poc/scripts/internal/messagepack"
)

type entry struct{ Source, Destination, SHA256, AdaptedSHA256 string }

func main() {
	cache, err := exec.Command("go", "env", "GOMODCACHE").Output()
	must(err)
	root := strings.TrimSpace(string(cache))
	var entries []entry
	for _, p := range []struct{ module, version, subdir, dest, license string }{{"github.com/tinylib/msgp", messagepack.Version, "msgp", "internal/thirdparty/msgp/msgp", "LICENSE"}, {"github.com/philhofer/fwd", messagepack.FwdVersion, "", "internal/thirdparty/fwd", "LICENSE.md"}} {
		src := filepath.Join(root, filepath.FromSlash(p.module+"@"+p.version))
		base := filepath.Join(src, p.subdir)
		must(filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(base, path)
			if err != nil {
				return err
			}
			if !strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, ".s") && !strings.Contains(rel, "testdata") {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(raw)
			out := rewrite(raw)
			dst := filepath.Join(p.dest, rel)
			if err = os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
				return err
			}
			if err = os.WriteFile(dst, out, 0644); err != nil {
				return err
			}
			adapted := sha256.Sum256(out)
			entries = append(entries, entry{p.module + "@" + p.version + "/" + filepath.ToSlash(filepath.Join(p.subdir, rel)), filepath.ToSlash(dst), hex.EncodeToString(sum[:]), hex.EncodeToString(adapted[:])})
			return nil
		}))
		raw, err := os.ReadFile(filepath.Join(src, p.license))
		must(err)
		must(os.WriteFile(filepath.Join(licenseDirectory(p.dest), p.license), raw, 0644))
	}
	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	must(err)
	license, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(goroot)), "LICENSE"))
	must(err)
	must(os.WriteFile("internal/thirdparty/msgp/LICENSE-go", license, 0644))

	for _, origin := range []string{"msgp", "fwd"} {
		var selected []entry
		prefix := "internal/thirdparty/" + origin + "/"
		for _, item := range entries {
			if strings.HasPrefix(item.Destination, prefix) {
				selected = append(selected, item)
			}
		}
		b, err := json.MarshalIndent(selected, "", "  ")
		must(err)
		must(os.WriteFile(filepath.Join("internal/thirdparty", origin, "COPY.json"), append(b, '\n'), 0644))
	}
	fmt.Println("Run the provenance rehash/audit after reviewing regenerated source changes.")
	fmt.Printf("Copied %d pinned source files; import paths and test generator directive relocated.\n", len(entries))
}
func rewrite(b []byte) []byte {
	b = bytes.ReplaceAll(b, []byte("//go:generate msgp -o=defgen_test.go -tests=false"), []byte("//go:generate go run ../../../../scripts/msgpackgen -file=defs_test.go -o=defgen_test.go -tests=false"))
	return messagepack.RelocateImports(b)
}

// Runtime sources keep their original msgp/ subtree; licenses live at its origin root.
func licenseDirectory(destination string) string {
	if destination == "internal/thirdparty/msgp/msgp" {
		return filepath.Dir(destination)
	}
	return destination
}

func must(e error) {
	if e != nil {
		panic(e)
	}
}
