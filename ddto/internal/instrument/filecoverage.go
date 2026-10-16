package instrument

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// File coverage (ddto's --experimental-file-coverage) replaces Go's coverage
// counters as the source of per-test coverage with one mark per source file.
// A mark site raises the file's mark to Mini's current epoch. Mini advances
// the epoch when a test starts and, after the test, reports the files whose
// mark reached the test's epoch.
//
// Sites go where code can start running during a test: at the start of every
// statement list, after every statement that ends a basic block, as cover
// counts, and after every statement with a call or a channel operation,
// where a goroutine that blocked before the test can resume during it.
// Between two sites, code waits for no other goroutine, though the runtime
// can still suspend it briefly, to preempt it or to assist the garbage
// collector, as it can inside any statement. A site uses only
// sync/atomic's intrinsics, so it costs the compiler's inliner little, and
// it raises the mark with a compare-and-swap loop, so a mark never goes down.

// FileCoverageBankVersion is the layout of the bank records that instrumented
// packages register with Mini. Mini ignores the marks of other versions.
const FileCoverageBankVersion = 1

// Identifiers of the generated code. A user identifier with this prefix makes
// the file unsupported.
const fileCoveragePrefix = "__ddto_"

// FileCoverageRefusal reports why a Go file cannot carry marks. Such a file
// is reported for every test instead.
type FileCoverageRefusal string

// FileCoverageNameClash is the refusal of a file that declares an identifier
// with the generated code's prefix, which its package's bank could redeclare:
// the build then keeps Go's coverage counters.
const FileCoverageNameClash FileCoverageRefusal = "identifier with prefix " + fileCoveragePrefix

// directivesWithoutMarks are function directives under which a call or an
// atomic load may be unsafe or change behavior.
var directivesWithoutMarks = []string{"//go:nosplit", "//go:norace", "//go:systemstack", "//go:nowritebarrier", "//go:nowritebarrierrec", "//go:yeswritebarrierrec", "//go:nocheckptr", "//go:uintptrkeepalive", "//go:uintptrescapes"}

// MarkFileCoverage returns src with sites that raise mark index of the mark
// array marks, an import of sync/atomic for them and the logical name kept
// in a line directive, or why the file stays unmarked.
func MarkFileCoverage(logical string, src []byte, marks string, index int) ([]byte, FileCoverageRefusal) {
	p, m := fileCoveragePrefix, fmt.Sprintf("&%s[%d]", marks, index)
	site := fmt.Sprintf("if %se := %satomic.LoadUint64(&%sepoch); %satomic.LoadUint64(%s) < %se { for { %so := %satomic.LoadUint64(%s); if %so >= %se || %satomic.CompareAndSwapUint64(%s, %so, %se) { break } } };",
		p, p, p, p, m, p, p, p, m, p, p, p, m, p, p)
	return markFileCoverage(logical, src, site, "; import "+p+"atomic \"sync/atomic\"", false)
}

// MarkContextFileCoverage returns src with context sites, which set file
// index's bit in the current goroutine's file set: bit index%64 of word
// base+index/64, base being the bank's first word. Imports of sync/atomic
// and runtime come with them, and the logical name stays in a line
// directive. A file with line directives of its own stays unmarked: Mini
// maps the frames of a goroutine's stack to files by the name that the
// header gives.
func MarkContextFileCoverage(logical string, src []byte, base string, index int) ([]byte, FileCoverageRefusal) {
	p, w, bit := fileCoveragePrefix, fmt.Sprintf("%srt.DdtoWord(%s + %d)", fileCoveragePrefix, base, index/64), fmt.Sprintf("%#x", uint64(1)<<(index%64))
	site := fmt.Sprintf("if %sw := %s; %satomic.LoadUint64(%sw)&%s == 0 { for { %so := %satomic.LoadUint64(%sw); if %so&%s != 0 || %satomic.CompareAndSwapUint64(%sw, %so, %so|%s) { break } } };",
		p, w, p, p, bit, p, p, p, p, bit, p, p, p, p, bit)
	return markFileCoverage(logical, src, site, "; import "+p+"atomic \"sync/atomic\"; import "+p+"rt \"runtime\"", true)
}

// markFileCoverage inserts site where code can start running, and imports
// after the package clause. With noLineDirectives, a file with line
// directives of its own stays unmarked.
func markFileCoverage(logical string, src []byte, site, imports string, noLineDirectives bool) ([]byte, FileCoverageRefusal) {
	// A byte order mark is valid only at the start of the file, before the
	// line directive goes: the rewritten file leaves it out.
	src = bytes.TrimPrefix(src, []byte("\uFEFF"))
	fs := token.NewFileSet()
	file, err := parser.ParseFile(fs, logical, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, FileCoverageRefusal("parse: " + err.Error())
	}
	if declaresFileCoveragePrefix(file) {
		return nil, FileCoverageNameClash
	}
	for _, spec := range file.Imports {
		if path, _ := strconv.Unquote(spec.Path.Value); path == "C" {
			return nil, "cgo"
		}
	}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if noLineDirectives && (strings.HasPrefix(comment.Text, "//line ") || strings.HasPrefix(comment.Text, "/*line ")) {
				return nil, "line directive"
			}
			for _, directive := range directivesWithoutMarks {
				if comment.Text == directive || strings.HasPrefix(comment.Text, directive+" ") {
					return nil, FileCoverageRefusal("directive " + directive)
				}
			}
		}
	}
	offset := func(p token.Pos) int { return fs.Position(p).Offset }
	// The imports go on the package clause's line, so no line moves.
	edits := []edit{{offset(file.Name.End()), offset(file.Name.End()), imports}}
	insert := func(p token.Pos) { edits = append(edits, edit{offset(p), offset(p), site}) }
	// list marks a statement list that starts at start.
	var list func(start token.Pos, stmts []ast.Stmt)
	list = func(start token.Pos, stmts []ast.Stmt) {
		insert(start)
		for i, stmt := range stmts {
			if label, ok := stmt.(*ast.LabeledStmt); ok && !isControl(label.Stmt) {
				// A goto may reach the statement through its label: mark it
				// between the two, as cover counts it.
				insert(label.Colon + 1)
				continue
			}
			if i > 0 && (endsBasicBlock(stmts[i-1]) || mayBlock(stmts[i-1])) {
				insert(stmt.Pos())
			}
		}
	}
	// switch and select bodies hold only clauses, even when empty.
	clauses := map[*ast.BlockStmt]bool{}
	code := false
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			// Function literals outside such declarations, as in a variable's
			// initializer, may run only while the package initializes.
			if n.Body != nil && (n.Recv != nil || n.Name.Name != "init") {
				code = true
			}
			return n.Body != nil
		case *ast.SwitchStmt:
			clauses[n.Body] = true
		case *ast.TypeSwitchStmt:
			clauses[n.Body] = true
		case *ast.SelectStmt:
			clauses[n.Body] = true
		case *ast.BlockStmt:
			if !clauses[n] {
				list(n.Lbrace+1, n.List)
			}
		case *ast.CaseClause:
			list(n.Colon+1, n.Body)
		case *ast.CommClause:
			list(n.Colon+1, n.Body)
		}
		return true
	})
	if !code {
		// Package initialization runs before any test: a file whose only
		// code is its declarations' initialization and init functions never
		// marks, although tests depend on what it declares.
		return nil, "no code that runs after package initialization"
	}
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].start < edits[j].start })
	var buf bytes.Buffer
	header := "//line " + logical + ":1\n"
	buf.Grow(len(header) + len(src) + len(edits)*len(site))
	buf.WriteString(header)
	cursor := 0
	for _, e := range edits {
		buf.Write(src[cursor:e.start])
		buf.WriteString(e.text)
		cursor = e.end
	}
	buf.Write(src[cursor:])
	return buf.Bytes(), ""
}

// FileCoverageNameClashError reports a Go file that the build compiles and
// that declares or uses an identifier with the generated code's prefix, or
// nil. Every compiled file of a package is checked, marked or not: the bank
// declares package-level names with the prefix.
func FileCoverageNameClashError(logical string, src []byte) error {
	if !bytes.Contains(src, []byte(fileCoveragePrefix)) {
		return nil
	}
	file, err := parser.ParseFile(token.NewFileSet(), logical, bytes.TrimPrefix(src, []byte("\uFEFF")), parser.SkipObjectResolution)
	if err != nil || !declaresFileCoveragePrefix(file) {
		return nil
	}
	return fmt.Errorf("%s: %s", logical, FileCoverageNameClash)
}

func declaresFileCoveragePrefix(file *ast.File) bool {
	prefixed := false
	ast.Inspect(file, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && strings.HasPrefix(id.Name, fileCoveragePrefix) {
			prefixed = true
		}
		return !prefixed
	})
	return prefixed
}

// endsBasicBlock reports a statement after which cover starts a new basic
// block: control flow, a label, a panic, or a function literal, whose body is
// a block of its own.
func endsBasicBlock(s ast.Stmt) bool {
	switch s := s.(type) {
	case *ast.BlockStmt, *ast.BranchStmt, *ast.ForStmt, *ast.IfStmt, *ast.LabeledStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.SelectStmt, *ast.TypeSwitchStmt:
		return true
	case *ast.ExprStmt:
		if call, ok := s.X.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "panic" {
				return true
			}
		}
	}
	literal := false
	ast.Inspect(s, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			literal = true
		}
		return !literal
	})
	return literal
}

// mayBlock reports a statement with a call or a channel operation, outside
// function literals: a goroutine can block in it and resume later. Without
// types, every call counts, a builtin's or a conversion's too, which only
// adds sites.
func mayBlock(s ast.Stmt) bool {
	found := false
	ast.Inspect(s, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.CallExpr, *ast.SendStmt:
			found = true
		case *ast.UnaryExpr:
			if n.Op == token.ARROW {
				found = true
			}
		}
		return !found
	})
	return found
}

// isControl reports a statement that a labeled break or continue can target,
// whose label must stay attached to it.
func isControl(s ast.Stmt) bool {
	switch s.(type) {
	case *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
		return true
	}
	return false
}

// FileCoverageBank is one generated bank file of a package variant: the
// marks of its files and the names of the files that every test reports.
type FileCoverageBank struct {
	// Package is the package clause, such as "p" or "p_test".
	Package string
	// Marks is the name of the mark array, which the sites index.
	Marks string
	// Names are the coverage names of the marked files, in mark order.
	Names []string
	// Always are the coverage names that every test reports.
	Always []string
	// Shared declares the epoch and the bank list, which a package variant
	// declares once.
	Shared bool
	// Context renders the bank of context file coverage
	// (MarkContextFileCoverage): Marks names the bank's first word, which
	// the bank reserves while its package initializes.
	Context bool
	// Prefix is the package's symbol prefix, which Mini compares with its
	// stack frames' function names, and Path the import path that -trimpath
	// gives the package's directory (context banks only).
	Prefix, Path string
}

// FileCoverageContextBankVersion is the layout of context banks.
const FileCoverageContextBankVersion = 2

// Source renders the bank file. Instrumented packages only read and write
// Mini's variables: none of their code calls Mini or testing, which may not
// have initialized yet. The code compiles with any Go version that a module
// can declare.
func (b FileCoverageBank) Source() string {
	if b.Context {
		return b.contextSource()
	}
	var s strings.Builder
	fmt.Fprintf(&s, "package %s\n\nimport (\n\t%satomic \"sync/atomic\"\n\t%sunsafe \"unsafe\"\n)\n\n", b.Package, fileCoveragePrefix, fileCoveragePrefix)
	p := fileCoveragePrefix
	if b.Shared {
		fmt.Fprintf(&s, "//go:linkname %sepoch %s.fileCoverageEpoch\nvar %sepoch uint64\n\n", p, FileCoveragePackage, p)
		fmt.Fprintf(&s, "//go:linkname %sbanks %s.fileCoverageBanksV1\nvar %sbanks %sunsafe.Pointer\n\n", p, FileCoveragePackage, p, p)
	}
	n, always := len(b.Names), len(b.Always)
	marksRef, namesRef, alwaysRef := "nil", "nil", "nil"
	if n > 0 {
		fmt.Fprintf(&s, "var %s [%d]uint64\n\nvar %snames = [%d]string{", b.Marks, n, b.Marks, n)
		for _, name := range b.Names {
			fmt.Fprintf(&s, "%q, ", name)
		}
		s.WriteString("}\n\n")
		marksRef, namesRef = "&"+b.Marks+"[0]", "&"+b.Marks+"names[0]"
	}
	if always > 0 {
		fmt.Fprintf(&s, "var %salways = [%d]string{", b.Marks, always)
		for _, name := range b.Always {
			fmt.Fprintf(&s, "%q, ", name)
		}
		s.WriteString("}\n\n")
		alwaysRef = "&" + b.Marks + "always[0]"
	}
	fmt.Fprintf(&s, "var %sbank = struct {\n\tnext    %sunsafe.Pointer\n\tversion uintptr\n\tmarks   *uint64\n\tnames   *string\n\tn       uintptr\n\talways  *string\n\tnAlways uintptr\n}{nil, %d, %s, %s, %d, %s, %d}\n\n", b.Marks, p, FileCoverageBankVersion, marksRef, namesRef, n, alwaysRef, always)
	fmt.Fprintf(&s, "func init() {\n\tbank := %sunsafe.Pointer(&%sbank)\n\tfor {\n\t\thead := %satomic.LoadPointer(&%sbanks)\n\t\t%sbank.next = head\n\t\tif %satomic.CompareAndSwapPointer(&%sbanks, head, bank) {\n\t\t\treturn\n\t\t}\n\t}\n}\n", p, b.Marks, p, p, b.Marks, p, p)
	return s.String()
}

// contextSource renders a context bank. The bank reserves its words with a
// variable's initializer, which runs before any of its package's code can
// reach a site, and registers itself in init, as the banks of marks do.
func (b FileCoverageBank) contextSource() string {
	var s strings.Builder
	p := fileCoveragePrefix
	fmt.Fprintf(&s, "package %s\n\nimport (\n\t%satomic \"sync/atomic\"\n\t%sunsafe \"unsafe\"\n)\n\n", b.Package, p, p)
	if b.Shared {
		fmt.Fprintf(&s, "//go:linkname %swords %s.fileCoverageWords\nvar %swords uint64\n\n", p, FileCoveragePackage, p)
		fmt.Fprintf(&s, "//go:linkname %sbanks %s.fileCoverageBanksV2\nvar %sbanks %sunsafe.Pointer\n\n", p, FileCoveragePackage, p, p)
		fmt.Fprintf(&s, "func %sreserve(files uintptr) uintptr {\n\twords := uint64(files+63) / 64\n\treturn uintptr(%satomic.AddUint64(&%swords, words) - words)\n}\n\n", p, p, p)
	}
	n, always := len(b.Names), len(b.Always)
	fmt.Fprintf(&s, "var %s = %sreserve(%d)\n\n", b.Marks, p, n)
	namesRef, alwaysRef := "nil", "nil"
	if n > 0 {
		fmt.Fprintf(&s, "var %snames = [%d]string{", b.Marks, n)
		for _, name := range b.Names {
			fmt.Fprintf(&s, "%q, ", name)
		}
		s.WriteString("}\n\n")
		namesRef = "&" + b.Marks + "names[0]"
	}
	if always > 0 {
		fmt.Fprintf(&s, "var %salways = [%d]string{", b.Marks, always)
		for _, name := range b.Always {
			fmt.Fprintf(&s, "%q, ", name)
		}
		s.WriteString("}\n\n")
		alwaysRef = "&" + b.Marks + "always[0]"
	}
	fmt.Fprintf(&s, "var %sbank = struct {\n\tnext    %sunsafe.Pointer\n\tversion uintptr\n\tbase    *uintptr\n\tnames   *string\n\tn       uintptr\n\talways  *string\n\tnAlways uintptr\n\tprefix  string\n\tpath    string\n}{nil, %d, &%s, %s, %d, %s, %d, %q, %q}\n\n", b.Marks, p, FileCoverageContextBankVersion, b.Marks, namesRef, n, alwaysRef, always, b.Prefix, b.Path)
	fmt.Fprintf(&s, "func init() {\n\tbank := %sunsafe.Pointer(&%sbank)\n\tfor {\n\t\thead := %satomic.LoadPointer(&%sbanks)\n\t\t%sbank.next = head\n\t\tif %satomic.CompareAndSwapPointer(&%sbanks, head, bank) {\n\t\t\treturn\n\t\t}\n\t}\n}\n", p, b.Marks, p, p, b.Marks, p, p)
	return s.String()
}

// fileCoverageRuntimeAPI is what the selected runtime's coverage package
// declares when it reads banks of FileCoverageBankVersion's layout.
var fileCoverageRuntimeAPI = []string{"var fileCoverageEpoch uint64", "var fileCoverageBanksV1 unsafe.Pointer"}

// FileCoverageRuntimeError reports why the selected runtime, whose coverage
// package is given as the sources that the build compiles, does not read
// file marks, or nil.
func FileCoverageRuntimeError(sources map[string][]byte) error {
	return declarationsError(sources, fileCoverageRuntimeAPI, "the runtime's coverage package lacks %s")
}

// fileCoverageContextRuntimeAPI is what the selected runtime's coverage
// package declares when it reads context banks: the variables that the banks
// and the runtime's edits link to, with the types that they give them, and
// the functions that the singleflight edits call.
var fileCoverageContextRuntimeAPI = []string{
	"var fileCoverageWords uint64",
	"var fileCoverageBanksV2 unsafe.Pointer",
	"var fileCoverageUnknown unsafe.Pointer",
	"var fileCoverageGetState func() (unsafe.Pointer, unsafe.Pointer)",
	"var fileCoverageSetState func(unsafe.Pointer, unsafe.Pointer)",
	"var fileCoverageSendHook func(*uintptr, unsafe.Pointer) (unsafe.Pointer)",
	"var fileCoverageReceiveHook func(uintptr, unsafe.Pointer, unsafe.Pointer) (unsafe.Pointer, unsafe.Pointer, unsafe.Pointer)",
	"var fileCoverageIOHook func(unsafe.Pointer) (unsafe.Pointer)",
	"func fileCoverageEnter(*unsafe.Pointer)",
	"func fileCoverageLeave(*unsafe.Pointer)",
	"func fileCoverageJoin(*unsafe.Pointer)",
	"func fileCoverageComplete(*unsafe.Pointer)",
}

// FileCoverageContextRuntimeError reports why the selected runtime, whose
// coverage package is given as the sources that the build compiles, does not
// read context banks, or nil.
func FileCoverageContextRuntimeError(sources map[string][]byte) error {
	return declarationsError(sources, fileCoverageContextRuntimeAPI, "the runtime's coverage package lacks %s")
}

// FileCoveragePackage is the Mini package that declares the epoch and the
// bank list that instrumented packages link to.
const FileCoveragePackage = "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting/coverage"

// SymbolPrefix returns the prefix of the symbol names of the package with
// import path, as the linker escapes it (cmd/internal/objabi.PathToPrefix).
func SymbolPrefix(path string) string {
	slash := strings.LastIndex(path, "/")
	var out strings.Builder
	for i := 0; i < len(path); i++ {
		if c := path[i]; c <= ' ' || c == '.' && i > slash || c == '%' || c == '"' || c >= 0x7F {
			fmt.Fprintf(&out, "%%%02x", c)
		} else {
			out.WriteByte(c)
		}
	}
	return out.String()
}
