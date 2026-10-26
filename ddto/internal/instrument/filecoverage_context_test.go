package instrument

import (
	"encoding/json"
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// toolchainSources returns the Go files of the toolchain's package path that
// a build for this platform compiles.
func toolchainSources(t *testing.T, path string) (string, map[string][]byte) {
	t.Helper()
	return platformSources(t, build.Default, path)
}

// platformSources returns the Go files of the toolchain's package path that
// a build with ctxt's platform compiles.
func platformSources(t *testing.T, ctxt build.Context, path string) (string, map[string][]byte) {
	t.Helper()
	pkg, err := ctxt.Import(path, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string][]byte{}
	for _, name := range pkg.GoFiles {
		full := filepath.Join(pkg.Dir, name)
		if sources[full], err = os.ReadFile(full); err != nil {
			t.Fatal(err)
		}
	}
	return pkg.Dir, sources
}

// TestContextEditsKeepLines checks that every context edit applies to this
// toolchain's sources and keeps each line where it was.
func TestContextEditsKeepLines(t *testing.T) {
	for _, tc := range []struct {
		path      string
		transform func(map[string][]byte) (map[string][]byte, error)
	}{
		{"runtime", TransformContextRuntime},
		{"internal/poll", TransformContextPoll},
		{"context", TransformContextContext},
		{"internal/singleflight", TransformContextSingleflight},
		{"net/http", TransformContextHTTP},
		{"testing", TransformContextTesting},
	} {
		t.Run(tc.path, func(t *testing.T) {
			_, sources := toolchainSources(t, tc.path)
			edited, err := tc.transform(sources)
			if err != nil {
				t.Fatalf("%v; review the edits of %s", err, tc.path)
			}
			for name, src := range edited {
				original := strings.Split(string(sources[name]), "\n")
				got := strings.Split(string(src), "\n")
				if len(got) != len(original)+1 || got[0] != "//line "+name+":1" {
					t.Fatalf("%s: %d lines, want the line directive and %d", name, len(got), len(original))
				}
				for i, line := range original {
					if edits := got[i+1]; !strings.Contains(edits, "ddto") && edits != line {
						t.Fatalf("%s:%d: %q became %q", name, i+1, line, edits)
					}
				}
			}
		})
	}
	// Windows reads os.Pipe's pipes and consoles without the poller, and only
	// its sources name their kinds: check the edit on every host, tip included.
	t.Run("internal/poll for windows", func(t *testing.T) {
		ctxt := build.Default
		ctxt.GOOS = "windows"
		_, sources := platformSources(t, ctxt, "internal/poll")
		edited, err := TransformContextPoll(sources)
		if err != nil {
			t.Fatalf("%v; review the edits of internal/poll for Windows", err)
		}
		for _, src := range edited {
			if strings.Contains(string(src), ".kind == kindPipe || ") {
				return
			}
		}
		t.Fatal("the Windows edit of readLock does not cover pipes")
	})
}

// TestContextRuntimePropagates builds a program with the context edits and a
// stand-in for Mini's hooks, and checks how contexts flow: goroutines inherit
// them; a receive merges the senders' union, once, and a closed channel or a
// nil sender adds nothing; timers and context.AfterFunc run with their
// registrant's context; a pipe read leaves the goroutine without one.
func TestContextRuntimePropagates(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the runtime")
	}
	dir := t.TempDir()
	if keep := os.Getenv("DDTO_CONTEXT_KEEP"); keep != "" {
		dir = keep
	}
	const mini = "example.com/ctx/mini"
	overlay := map[string]string{}
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(dir, "overlay", strings.ReplaceAll(name, string(filepath.Separator), "_"))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for _, tc := range []struct {
		path      string
		transform func(map[string][]byte) (map[string][]byte, error)
		hook      string
	}{
		{"runtime", TransformContextRuntime, ContextRuntimeHook(mini)},
		{"internal/poll", TransformContextPoll, ContextPollHook},
		{"context", TransformContextContext, ContextCaptureHook("context")},
		{"net/http", TransformContextHTTP, ContextCaptureHook("http")},
	} {
		pkgDir, sources := toolchainSources(t, tc.path)
		edited, err := tc.transform(sources)
		if err != nil {
			t.Fatal(err)
		}
		for name, src := range edited {
			overlay[name] = write(name, string(src))
		}
		overlay[filepath.Join(pkgDir, "zz_ddto_context.go")] = write(filepath.Join(pkgDir, "zz_ddto_context.go"), tc.hook)
	}
	files := map[string]string{
		"go.mod": "module example.com/ctx\n\ngo 1.25\n",
		"mini/mini.go": `package mini

import (
	"sync"
	"unsafe"
)

var (
	fileCoverageUnknown     unsafe.Pointer
	fileCoverageSendHook    func(slot *uintptr, ctx unsafe.Pointer) unsafe.Pointer
	fileCoverageReceiveHook func(union uintptr, ctx, calls unsafe.Pointer) (unsafe.Pointer, unsafe.Pointer, unsafe.Pointer)
	fileCoverageIOHook      func(ctx unsafe.Pointer) unsafe.Pointer
	fileCoverageGetState    func() (unsafe.Pointer, unsafe.Pointer)
	fileCoverageSetState    func(unsafe.Pointer, unsafe.Pointer)
)

// A context is a set of owner names, joined by commas in name order.
var (
	mu       sync.Mutex
	interned = map[string]*[8]uint64{}
	names    = map[uintptr]string{}
	pointers = map[uintptr]unsafe.Pointer{}
	Calls    struct{ Send, Receive, NilReceive, IO int }
)

func Context(owners string) unsafe.Pointer {
	mu.Lock()
	defer mu.Unlock()
	return intern(owners)
}

func intern(owners string) unsafe.Pointer {
	if owners == "" {
		return nil
	}
	bits, ok := interned[owners]
	if !ok {
		bits = new([8]uint64)
		interned[owners] = bits
		names[uintptr(unsafe.Pointer(bits))] = owners
		pointers[uintptr(unsafe.Pointer(bits))] = unsafe.Pointer(bits)
	}
	return unsafe.Pointer(bits)
}

func union(a, b string) string {
	set := map[string]bool{}
	for _, s := range []string{a, b} {
		for _, owner := range splitOwners(s) {
			set[owner] = true
		}
	}
	var out []string
	for owner := range set {
		out = append(out, owner)
	}
	sortStrings(out)
	return joinOwners(out)
}

func Name(ctx unsafe.Pointer) string {
	mu.Lock()
	defer mu.Unlock()
	if ctx == nil {
		return "unknown"
	}
	return names[uintptr(ctx)]
}

func Current() string           { ctx, _ := fileCoverageGetState(); return Name(ctx) }
func Set(owners string)         { fileCoverageSetState(Context(owners), nil) }
func Unknown() unsafe.Pointer    { return fileCoverageUnknown }
func SetState(owners, calls string) { fileCoverageSetState(Context(owners), Context(calls)) }
func State() (string, string) {
	ctx, calls := fileCoverageGetState()
	return Name(ctx), Name(calls)
}

func init() {
	fileCoverageSendHook = func(slot *uintptr, ctx unsafe.Pointer) unsafe.Pointer {
		mu.Lock()
		defer mu.Unlock()
		Calls.Send++
		merged := intern(union(names[*slot], names[uintptr(ctx)]))
		*slot = uintptr(merged)
		return merged
	}
	fileCoverageReceiveHook = func(u uintptr, ctx, calls unsafe.Pointer) (unsafe.Pointer, unsafe.Pointer, unsafe.Pointer) {
		mu.Lock()
		defer mu.Unlock()
		if ctx == nil {
			Calls.NilReceive++
			return nil, calls, pointers[u]
		}
		Calls.Receive++
		return intern(union(names[u], names[uintptr(ctx)])), nil, pointers[u]
	}
	fileCoverageIOHook = func(ctx unsafe.Pointer) unsafe.Pointer {
		mu.Lock()
		defer mu.Unlock()
		Calls.IO++
		return nil
	}
}
`,
		"mini/strings.go": `package mini

import (
	"sort"
	"strings"
)

func splitOwners(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
func joinOwners(s []string) string { return strings.Join(s, ",") }
func sortStrings(s []string)       { sort.Strings(s) }
`,
		"main.go": `package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"runtime/trace"
	"time"

	"example.com/ctx/mini"
)

func expect(what, got, want string) {
	if got != want {
		fmt.Printf("FAIL %s: %s, want %s\n", what, got, want)
		os.Exit(1)
	}
}

func in(owners string, f func()) string {
	done := make(chan string)
	go func() {
		mini.Set(owners)
		f()
		done <- mini.Current()
	}()
	return <-done
}

func main() {
	expect("main", mini.Current(), "unknown")
	expect("scratch word", fmt.Sprint(runtime.DdtoWord(3) == (*uint64)(unsafe_add(mini.Unknown(), 24))), "true")

	// A goroutine starts with its creator's context.
	expect("inherit", in("A", func() {
		got := make(chan string)
		go func() { got <- mini.Current() }()
		expect("child", <-got, "A")
	}), "A")

	// A receive adds the senders' union; a nil sender and a closed
	// channel add nothing.
	ch := make(chan int, 1)
	in("B", func() { ch <- 1 })
	expect("receive", in("A", func() { <-ch }), "A,B")
	closed := make(chan int)
	close(closed)
	expect("closed", in("A", func() { <-closed }), "A")
	nilSent := make(chan int, 1)
	nilSent <- 1
	expect("nil sender", in("A", func() { <-nilSent }), "A")
	sel := make(chan int, 1)
	in("C", func() { sel <- 1 })
	expect("select", in("A", func() {
		select {
		case <-sel:
		case <-time.After(time.Second):
		}
	}), "A,C")
	unbuffered := make(chan int)
	go func() { mini.Set("D"); unbuffered <- 1 }()
	expect("handoff", in("A", func() { <-unbuffered }), "A,D")

	// Timers deliver and run with their creator's context.
	var timer <-chan time.Time
	in("E", func() { timer = time.After(time.Millisecond) })
	expect("time.After", in("A", func() { <-timer }), "A,E")
	fired := make(chan string, 1)
	in("F", func() { time.AfterFunc(time.Millisecond, func() { fired <- mini.Current() }) })
	expect("time.AfterFunc", <-fired, "F")
	ctx, cancel := context.WithCancel(context.Background())
	in("G", func() { context.AfterFunc(ctx, func() { fired <- mini.Current() }) })
	in("H", cancel)
	expect("context.AfterFunc", <-fired, "G")
	var server http.Server
	in("I", func() { server.RegisterOnShutdown(func() { fired <- mini.Current() }) })
	in("J", func() { server.Shutdown(context.Background()) })
	expect("RegisterOnShutdown", <-fired, "I")

	// A receiver without a context keeps none, and Mini sees the union.
	nilReceives := mini.Calls.NilReceive
	unionCh := make(chan int, 1)
	in("K", func() { unionCh <- 1 })
	<-unionCh
	expect("nil receiver", mini.Current(), "unknown")
	expect("nil receive hook", fmt.Sprint(mini.Calls.NilReceive-nilReceives), "1")

	// The runtime's own channels under its locks call no hook.
	in("L", func() {
		if err := trace.Start(io.Discard); err != nil {
			panic(err)
		}
		trace.Stop()
	})

	// A socket read leaves the goroutine without a context, for its life;
	// a regular file's does not.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			conn.Write([]byte("x"))
		}
	}()
	expect("socket", in("A", func() {
		conn, err := net.Dial("tcp", listener.Addr().String())
		if err != nil {
			panic(err)
		}
		conn.Read(make([]byte, 1))
	}), "unknown")
	expect("io hook", fmt.Sprint(mini.Calls.IO), "1")
	expect("file", in("A", func() { os.ReadFile("go.mod") }), "A")
	// So does a pipe read, such as a worker's: Windows does not poll pipes.
	pipeReader, pipeWriter, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	go pipeWriter.Write([]byte("x"))
	expect("pipe", in("A", func() { pipeReader.Read(make([]byte, 1)) }), "unknown")
	expect("pipe io hook", fmt.Sprint(mini.Calls.IO), "2")

	// A goroutine without a context passes its calls to its children and its
	// timers, and its sends carry them.
	got := make(chan string, 1)
	go func() {
		mini.SetState("", "C1")
		go func() { _, calls := mini.State(); got <- calls }()
	}()
	expect("calls inherit", <-got, "C1")
	go func() {
		mini.SetState("", "C2")
		time.AfterFunc(time.Millisecond, func() { _, calls := mini.State(); got <- calls })
	}()
	expect("calls timer", <-got, "C2")
	callsCh := make(chan int, 1)
	go func() {
		mini.SetState("", "C3")
		callsCh <- 1
		got <- "sent"
	}()
	<-got
	expect("calls send", in("A", func() { <-callsCh }), "A,C3")

	// A goroutine that uses its own channel calls Mini once.
	own := make(chan int, 1)
	calls := make(chan string, 1)
	in("A", func() {
		send, receive := mini.Calls.Send, mini.Calls.Receive
		for i := 0; i < 100; i++ {
			own <- i
			<-own
		}
		calls <- fmt.Sprint(mini.Calls.Send-send, mini.Calls.Receive-receive)
	})
	expect("cached", <-calls, "1 0")
	fmt.Println("ok")
}
`,
		"add.go": `package main

import "unsafe"

func unsafe_add(p unsafe.Pointer, n uintptr) unsafe.Pointer { return unsafe.Add(p, n) }
`,
	}
	for name, content := range files {
		path := filepath.Join(dir, "src", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(map[string]any{"Replace": overlay})
	if err != nil {
		t.Fatal(err)
	}
	overlayFile := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(overlayFile, data, 0o644); err != nil {
		t.Fatal(err)
	}
	// The toolchain's own go command, with a module cache that does not hold
	// its GOROOT: go refuses to replace files beneath the module cache.
	cmd := exec.Command(filepath.Join(build.Default.GOROOT, "bin", "go"), "run", "-overlay="+overlayFile, ".")
	cmd.Dir = filepath.Join(dir, "src")
	env := append(os.Environ(), "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local", "GOMODCACHE="+filepath.Join(dir, "modcache"))
	// Before Go 1.27, timer channels of modules before Go 1.23 are
	// asynchronous; Go 1.27 removed the setting.
	godebugs := []string{""}
	if sleep, err := os.ReadFile(filepath.Join(build.Default.GOROOT, "src", "time", "sleep.go")); err == nil && strings.Contains(string(sleep), "asynctimerchan") {
		godebugs = append(godebugs, "asynctimerchan=1")
	}
	for _, godebug := range godebugs {
		cmd := exec.Command(cmd.Path, cmd.Args[1:]...)
		cmd.Dir = filepath.Join(dir, "src")
		cmd.Env = append(env, "GODEBUG="+godebug)
		out, err := cmd.CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != "ok" {
			t.Fatalf("GODEBUG=%s: %v\n%s", godebug, err, out)
		}
	}
}
