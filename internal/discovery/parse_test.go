package discovery

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseTestsFile_MalformedJSON(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "tests.json")
	if err := os.WriteFile(filePath, []byte("{invalid json}\n"), 0644); err != nil {
		t.Fatalf("failed to write discovery file: %v", err)
	}

	if _, err := parseTestsFile(filePath); err == nil {
		t.Fatal("expected malformed JSON to fail")
	}
}

func BenchmarkParseTestsFile200000(b *testing.B) {
	const testCount = 200_000

	filePath := filepath.Join(b.TempDir(), "tests.json")
	fileSize := writeBenchmarkDiscoveryFile(b, filePath, testCount)

	b.Run("parse", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(fileSize)

		var retainedBytes uint64
		for i := 0; i < b.N; i++ {
			before := heapAllocAfterGC(b)

			b.StartTimer()
			tests, err := parseTestsFile(filePath)
			b.StopTimer()
			if err != nil {
				b.Fatalf("parseTestsFile() failed: %v", err)
			}
			if len(tests) != testCount {
				b.Fatalf("parsed %d tests, want %d", len(tests), testCount)
			}

			after := heapAllocAfterGC(b)
			retainedBytes += heapDelta(before, after)
			runtime.KeepAlive(tests)
		}
		b.ReportMetric(float64(retainedBytes)/float64(b.N)/(1024*1024), "MiB_retained/op")
	})
}

func writeBenchmarkDiscoveryFile(b *testing.B, filePath string, testCount int) int64 {
	b.Helper()

	file, err := os.Create(filePath)
	if err != nil {
		b.Fatalf("failed to create benchmark discovery file: %v", err)
	}
	writer := bufio.NewWriterSize(file, 1024*1024)
	for i := 0; i < testCount; i++ {
		if _, err := fmt.Fprintf(
			writer,
			`{"module":"rspec","suite":"Suite%06d","name":"test_%06d","parameters":"{\"scoped_id\":\"%06d\"}","suiteSourceFile":"spec/models/model_%06d_spec.rb"}`+"\n",
			i%20_000,
			i,
			i,
			i%20_000,
		); err != nil {
			b.Fatalf("failed to write benchmark discovery record: %v", err)
		}
	}
	if err := writer.Flush(); err != nil {
		b.Fatalf("failed to flush benchmark discovery file: %v", err)
	}
	info, err := file.Stat()
	if err != nil {
		b.Fatalf("failed to stat benchmark discovery file: %v", err)
	}
	if err := file.Close(); err != nil {
		b.Fatalf("failed to close benchmark discovery file: %v", err)
	}
	return info.Size()
}

func heapAllocAfterGC(b *testing.B) uint64 {
	b.Helper()
	b.StopTimer()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

func heapDelta(before, after uint64) uint64 {
	if after <= before {
		return 0
	}
	return after - before
}
