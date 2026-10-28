package citransport

import (
	"bytes"
	"compress/gzip"
	"sync"
)

// Compressor reuse avoids allocating flate's tables for every CI batch. Large
// output buffers are discarded so one unusually large payload is not retained.
type gzipCompressor struct {
	buffer bytes.Buffer
	writer *gzip.Writer
}

var gzipCompressors = sync.Pool{New: func() any {
	compressor := &gzipCompressor{}
	// CI batches favor compression speed. BestSpeed still emits ordinary gzip;
	// intake limits are checked against the uncompressed MessagePack payload.
	writer, err := gzip.NewWriterLevel(&compressor.buffer, gzip.BestSpeed)
	if err != nil {
		panic(err) // The constant compression level is always valid.
	}
	compressor.writer = writer
	return compressor
}}

func releaseCompressor(compressor *gzipCompressor) {
	if compressor.buffer.Cap() > TestCycleFlushBytes {
		compressor.buffer = bytes.Buffer{}
	} else {
		compressor.buffer.Reset()
	}
	gzipCompressors.Put(compressor)
}
