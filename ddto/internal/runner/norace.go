package runner

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// appendNorace appends src to dst with each function declaration that has a
// body marked //go:norace, so that a -race build does not instrument Mini's
// own memory accesses. A -race=false compile would remove more, but it also
// turns Mini's sync/atomic calls into plain instructions: the detector would
// lose the ordering they establish and report races in maps that Mini
// publishes atomically. Annotated functions keep their atomics and locks
// visible.
//
// A /*line*/ directive gives each declaration its original position, so
// positions in stack traces and source metadata do not change. It returns dst,
// with false, for a file without function bodies, with its own line
// directives, or with syntax that the lexer cannot follow.
func appendNorace(dst, src []byte) ([]byte, bool) {
	if bytes.Contains(src, []byte("//line ")) || bytes.Contains(src, []byte("/*line ")) {
		return dst, false
	}
	l := noraceLexer{src: src}
	var declarations []int
	depth, nesting := 0, 0
	statement := true
	for {
		kind, start := l.next()
		switch kind {
		case noraceEOF:
			if depth != 0 || nesting != 0 || len(declarations) == 0 {
				return dst, false
			}
			return appendDeclarations(dst, src, declarations), true
		case noraceError:
			return dst, false
		case noraceSemicolon:
			statement = true
			continue
		case noraceLBrace:
			depth++
		case noraceRBrace:
			depth--
		case noraceOpen:
			nesting++
		case noraceClose:
			nesting--
		case noraceFunc:
			// A declaration starts a top-level statement; a function literal
			// in a variable's value does not.
			if depth == 0 && nesting == 0 && statement {
				body, ok := l.signature()
				if !ok {
					return dst, false
				}
				statement = !body
				if body {
					declarations = append(declarations, start)
					depth++
				}
				continue
			}
		}
		statement = false
	}
}

func appendDeclarations(out, src []byte, declarations []int) []byte {
	last, line := 0, 1
	for _, offset := range declarations {
		line += bytes.Count(src[last:offset], []byte{'\n'})
		column := offset - bytes.LastIndexByte(src[:offset], '\n')
		out = append(out, src[last:offset]...)
		if column != 1 {
			out = append(out, '\n')
		}
		out = append(out, "//go:norace\n/*line :"...)
		out = strconv.AppendInt(out, int64(line), 10)
		out = append(out, ':')
		out = strconv.AppendInt(out, int64(column), 10)
		out = append(out, "*/"...)
		last = offset
	}
	return append(out, src[last:]...)
}

// noraceLexer splits Go source into the few tokens that locate function
// declarations, inserting semicolons at newlines as the Go lexer does.
type noraceLexer struct {
	src    []byte
	offset int
	// semicolon reports whether a newline here ends a statement.
	semicolon bool
}

// noraceWord holds the bytes of identifiers, keywords and numbers. Bytes of
// multi-byte characters only occur in identifiers outside literals.
var noraceWord = func() (word [256]bool) {
	for c := range 256 {
		word[c] = c == '_' || c >= 0x80 || c >= '0' && c <= '9' || c|0x20 >= 'a' && c|0x20 <= 'z'
	}
	return word
}()

const (
	noraceEOF = iota
	noraceError
	noraceSemicolon
	noraceFunc
	noraceType // struct or interface
	noraceOpen
	noraceClose
	noraceLBrace
	noraceRBrace
	noraceOther
)

func (l *noraceLexer) next() (kind, start int) {
	src := l.src
	for l.offset < len(src) {
		start = l.offset
		c := src[start]
		switch {
		case c == '\n':
			l.offset++
			if l.semicolon {
				l.semicolon = false
				return noraceSemicolon, start
			}
		case c == ' ' || c == '\t' || c == '\r':
			l.offset++
		case c == '/' && start+1 < len(src) && src[start+1] == '/':
			if end := bytes.IndexByte(src[start:], '\n'); end >= 0 {
				l.offset = start + end
			} else {
				l.offset = len(src)
			}
		case c == '/' && start+1 < len(src) && src[start+1] == '*':
			end := bytes.Index(src[start+2:], []byte("*/"))
			if end < 0 {
				return noraceError, start
			}
			l.offset = start + end + 4
			// A comment that spans lines acts as a newline.
			if l.semicolon && bytes.IndexByte(src[start:l.offset], '\n') >= 0 {
				l.semicolon = false
				return noraceSemicolon, start
			}
		case c == '"' || c == '\'':
			i := start + 1
			for ; i < len(src) && src[i] != c; i++ {
				if src[i] == '\n' {
					return noraceError, start
				}
				if src[i] == '\\' {
					i++
				}
			}
			if i >= len(src) {
				return noraceError, start
			}
			l.offset, l.semicolon = i+1, true
			return noraceOther, start
		case c == '`':
			end := bytes.IndexByte(src[start+1:], '`')
			if end < 0 {
				return noraceError, start
			}
			l.offset, l.semicolon = start+end+2, true
			return noraceOther, start
		case noraceWord[c]:
			end := start + 1
			for end < len(src) && noraceWord[src[end]] {
				end++
			}
			l.offset = end
			switch string(src[start:end]) {
			case "func":
				l.semicolon = false
				return noraceFunc, start
			case "struct", "interface":
				l.semicolon = false
				return noraceType, start
			case "case", "chan", "const", "default", "defer", "else", "for", "go", "goto", "if",
				"import", "map", "package", "range", "select", "switch", "type", "var":
				l.semicolon = false
				return noraceOther, start
			}
			// Identifiers, numbers, break, continue, fallthrough and return.
			l.semicolon = true
			return noraceOther, start
		default:
			l.offset++
			switch c {
			case '(', '[':
				l.semicolon = false
				return noraceOpen, start
			case ')', ']':
				l.semicolon = true
				return noraceClose, start
			case '{':
				l.semicolon = false
				return noraceLBrace, start
			case '}':
				l.semicolon = true
				return noraceRBrace, start
			case ';':
				l.semicolon = false
				return noraceSemicolon, start
			case '+', '-':
				l.semicolon = false
				if l.offset < len(src) && src[l.offset] == c {
					l.offset++
					l.semicolon = true
				}
				return noraceOther, start
			}
			l.semicolon = false
			return noraceOther, start
		}
	}
	if l.semicolon {
		l.semicolon = false
		return noraceSemicolon, len(src)
	}
	return noraceEOF, len(src)
}

// signature scans a declaration's signature up to its body's opening brace,
// reporting false at the semicolon that ends a declaration without one.
// Braces inside the signature belong to struct and interface types.
func (l *noraceLexer) signature() (body, ok bool) {
	nesting := 0
	for {
		kind, _ := l.next()
		switch kind {
		case noraceEOF, noraceError:
			return false, false
		case noraceOpen:
			nesting++
		case noraceClose:
			nesting--
		case noraceType:
			if kind, _ = l.next(); kind != noraceLBrace {
				return false, false
			}
			for braces := 1; braces > 0; {
				switch kind, _ = l.next(); kind {
				case noraceEOF, noraceError:
					return false, false
				case noraceLBrace:
					braces++
				case noraceRBrace:
					braces--
				}
			}
		case noraceLBrace:
			return nesting == 0, nesting == 0
		case noraceSemicolon:
			if nesting == 0 {
				return false, true
			}
		}
	}
}

// noraceOverlay adds annotated copies of the selected Mini's sources to the
// overlay, starting from any replacement that the overlay already holds. Mini
// keeps full instrumentation when the named packages include Mini's own, so
// its tests still check it, and when client code imports Mini: memory that the
// client passes to Mini's API, such as a pointer tag value, must stay checked
// as under go test. A file that cannot be read or copied keeps its
// instrumentation.
func noraceOverlay(listed []goPackage, replacements map[string]string, temp string) int {
	for _, p := range listed {
		if p.Module != nil && p.Module.Path == miniModule {
			if !p.DepOnly && p.ImportPath != miniPackage {
				return 0
			}
			continue
		}
		for _, imports := range [][]string{p.Imports, p.TestImports, p.XTestImports} {
			for _, path := range imports {
				if path == miniModule || strings.HasPrefix(path, miniModule+"/") {
					return 0
				}
			}
		}
	}
	type source struct{ logical, actual, backing string }
	var sources []source
	for i, p := range listed {
		if p.Module == nil || p.Module.Path != miniModule || p.Error != nil {
			continue
		}
		dir := filepath.Join(temp, "norace", strconv.Itoa(i))
		for _, name := range p.GoFiles {
			logical := filepath.Join(p.Dir, name)
			actual, replaced := replacements[logical]
			if !replaced {
				actual = logical
			} else if actual == "" {
				continue
			}
			sources = append(sources, source{logical, actual, filepath.Join(dir, name)})
		}
	}
	annotated := make([]bool, len(sources))
	var next atomic.Int64
	var wg sync.WaitGroup
	// Each worker reuses its buffers: sources total megabytes, and garbage
	// collection would cost more than the annotation.
	for range min(runtime.GOMAXPROCS(0), 4, len(sources)) {
		wg.Go(func() {
			var src bytes.Buffer
			var out []byte
			for i := int(next.Add(1) - 1); i < len(sources); i = int(next.Add(1) - 1) {
				file, err := os.Open(sources[i].actual)
				if err != nil {
					continue
				}
				info, err := file.Stat()
				if err == nil {
					src.Reset()
					_, err = src.ReadFrom(file)
				}
				file.Close()
				var ok bool
				if out, ok = appendNorace(out[:0], src.Bytes()); err != nil || !ok {
					continue
				}
				if err = os.MkdirAll(filepath.Dir(sources[i].backing), 0700); err == nil {
					err = os.WriteFile(sources[i].backing, out, 0600)
				}
				// Go indexes a package directory by its files' sizes and
				// modification times, and not at all while one is newer than
				// two seconds: the source's time keeps that index reusable.
				if err == nil {
					err = os.Chtimes(sources[i].backing, time.Time{}, info.ModTime())
				}
				annotated[i] = err == nil
			}
		})
	}
	wg.Wait()
	files := 0
	for i, s := range sources {
		if annotated[i] {
			replacements[s.logical] = s.backing
			files++
		}
	}
	return files
}
