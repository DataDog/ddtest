package instrument

// CoverageHook is an overlay file for package testing that lets Mini's
// per-test coverage read the coverage counters in memory instead of asking
// testing/internal/testdeps for a text profile twice per test.
//
// It compiles against the toolchain's own internal/coverage packages and only
// exposes what they hold; Mini builds the text profile's layout from it.
//
//   - meta decodes the coverage meta-data of every registered package once:
//     the counter segment offsets, then per package its counter mode and
//     granularity, path and functions, and per function its name, file,
//     literal flag and units. Strings are interned in the first result.
//   - counters copies every counter segment with atomic loads, each segment
//     preceded by its length, and reports how many packages are registered.
//   - profile renders a copy as the text profile that
//     cfile.ProcessCoverTestDir would write, with the toolchain's cformat; for
//     nil, with every count at its maximum. Mini checks its layout against the
//     latter and, in its debug comparison, its results against the former.
const CoverageHook = `package testing

import (
	"bytes"
	"internal/coverage"
	"internal/coverage/cformat"
	"internal/coverage/decodemeta"
	"internal/coverage/rtcov"
	"sync/atomic"
	"unsafe"
)

//go:linkname __dd_civisibility_coverageCounterList internal/coverage/cfile.getCovCounterList
func __dd_civisibility_coverageCounterList() []rtcov.CovCounterBlob

//go:linkname __dd_civisibility_registerTestingCoverage github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/dd-trace-go/civisibility/integrations/gotesting/coverage.registerTestingCoverage
func __dd_civisibility_registerTestingCoverage(func() ([]string, []uint32, string), func([]uint32) ([]uint32, int), func([]uint32) []byte)

func init() {
	__dd_civisibility_registerTestingCoverage(__dd_civisibility_coverageMeta, __dd_civisibility_coverageCounters, __dd_civisibility_coverageProfile)
}

func __dd_civisibility_coverageMeta() ([]string, []uint32, string) {
	var strs []string
	index := map[string]uint32{}
	str := func(s string) uint32 {
		i, ok := index[s]
		if !ok {
			i = uint32(len(strs))
			index[s] = i
			strs = append(strs, s)
		}
		return i
	}
	records := []uint32{uint32(coverage.NumCtrsOffset), uint32(coverage.PkgIdOffset), uint32(coverage.FuncIdOffset), uint32(coverage.FirstCtrOffset), uint32(len(rtcov.Meta.List))}
	var fd coverage.FuncDesc
	for _, b := range rtcov.Meta.List {
		d, err := decodemeta.NewCoverageMetaDataDecoder(unsafe.Slice(b.P, int(b.Len)), true)
		if err != nil {
			return nil, nil, "meta-data: " + err.Error()
		}
		funcs := d.NumFuncs()
		records = append(records, str(coverage.CounterMode(b.CounterMode).String()), str(coverage.CounterGranularity(b.CounterGranularity).String()), str(d.PackagePath()), funcs)
		for fn := uint32(0); fn < funcs; fn++ {
			if err := d.ReadFunc(fn, &fd); err != nil {
				return nil, nil, "meta-data: " + err.Error()
			}
			lit := uint32(0)
			if fd.Lit {
				lit = 1
			}
			records = append(records, str(fd.Funcname), str(fd.Srcfile), lit, uint32(len(fd.Units)))
			for _, u := range fd.Units {
				records = append(records, u.StLine, u.StCol, u.EnLine, u.EnCol, u.NxStmts, u.Parent)
			}
		}
	}
	return strs, records, ""
}

func __dd_civisibility_coverageCounters(dst []uint32) ([]uint32, int) {
	dst = dst[:0]
	for _, segment := range __dd_civisibility_coverageCounterList() {
		counters := unsafe.Slice((*atomic.Uint32)(unsafe.Pointer(segment.Counters)), int(segment.Len))
		dst = append(dst, uint32(len(counters)))
		for i := range counters {
			dst = append(dst, counters[i].Load())
		}
	}
	return dst, len(rtcov.Meta.List)
}

func __dd_civisibility_coverageProfile(raw []uint32) []byte {
	type pkfunc struct {
		slot int
		fn   uint32
	}
	live := map[pkfunc][]uint32{}
	for rest := raw; len(rest) > 0; {
		n := int(rest[0])
		if 1+n > len(rest) {
			return nil
		}
		segment := rest[1 : 1+n]
		rest = rest[1+n:]
		for i := 0; i < len(segment); i++ {
			if segment[i] == 0 {
				continue
			}
			if i+int(coverage.FirstCtrOffset) > len(segment) {
				return nil
			}
			ctrs := int(segment[i+int(coverage.NumCtrsOffset)])
			pkg := int32(segment[i+int(coverage.PkgIdOffset)])
			fn := segment[i+int(coverage.FuncIdOffset)]
			start := i + int(coverage.FirstCtrOffset)
			if start+ctrs > len(segment) {
				return nil
			}
			counters := segment[start : start+ctrs]
			i = start + ctrs - 1
			for _, v := range counters {
				if v != 0 {
					if pkg <= 0 {
						return nil
					}
					live[pkfunc{int(pkg) - 1, fn}] = counters
					break
				}
			}
		}
	}
	mode := coverage.CounterMode(coverage.CtrModeInvalid)
	for _, b := range rtcov.Meta.List {
		if m := coverage.CounterMode(b.CounterMode); m != coverage.CtrModeRegOnly && m != coverage.CtrModeTestMain {
			mode = m
			break
		}
	}
	if mode == coverage.CtrModeInvalid {
		return nil
	}
	formatter := cformat.NewFormatter(mode)
	var fd coverage.FuncDesc
	for slot, b := range rtcov.Meta.List {
		d, err := decodemeta.NewCoverageMetaDataDecoder(unsafe.Slice(b.P, int(b.Len)), true)
		if err != nil {
			return nil
		}
		formatter.SetPackage(d.PackagePath())
		for fn := uint32(0); fn < d.NumFuncs(); fn++ {
			if err := d.ReadFunc(fn, &fd); err != nil {
				return nil
			}
			counters, have := live[pkfunc{slot, fn}]
			for i, u := range fd.Units {
				if u.Parent != 0 {
					continue
				}
				count := uint32(0)
				switch {
				case raw == nil:
					count = ^uint32(0)
				case have && i < len(counters):
					count = counters[i]
				}
				formatter.AddUnit(fd.Srcfile, fd.Funcname, fd.Lit, u, count)
			}
		}
	}
	var text bytes.Buffer
	if err := formatter.EmitTextual(nil, &text); err != nil {
		return nil
	}
	return text.Bytes()
}
`
