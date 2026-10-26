package cachefunk

import (
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
)

// Compression compresses cached values before storage. CompressAndWrite and
// ReadAndDecompress stream, so a value is not held in memory twice.
type Compression interface {
	// Compress returns the compressed form of input.
	Compress(input []byte) ([]byte, error)
	// Decompress reverses Compress.
	Decompress(input []byte) ([]byte, error)
	// CompressAndWrite writes the compressed form of data to w.
	CompressAndWrite(w io.Writer, data []byte) error
	// ReadAndDecompress reads compressed data from r and returns it decompressed.
	ReadAndDecompress(r io.Reader) ([]byte, error)
	// String is the name used to select the compression in configuration.
	String() string
}

type noCompression struct{}

// NoCompression stores values unchanged.
var NoCompression = &noCompression{}

func (g *noCompression) CompressAndWrite(w io.Writer, data []byte) error {
	_, err := w.Write(data)
	return err
}

func (g *noCompression) ReadAndDecompress(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}

func (g *noCompression) Compress(input []byte) ([]byte, error) {
	return input, nil
}

func (g *noCompression) Decompress(input []byte) ([]byte, error) {
	return input, nil
}

func (g *noCompression) String() string {
	return "none"
}

type zstdCompression struct{}

// ZstdCompression compresses values with zstd and is the default. An empty value
// becomes a valid frame, so it reads back as an empty value rather than a missing
// entry.
var ZstdCompression = &zstdCompression{}

var (
	zstdEncoder = newZstdEncoder()
	zstdDecoder = newZstdDecoder()
)

func newZstdEncoder() *zstd.Encoder {
	// WithZeroFrames means an empty payload compresses to a valid, non-empty frame,
	// so an empty cached value is not confused with a missing entry.
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithZeroFrames(true))
	if err != nil {
		// cannot fail: the options above are valid
		panic(fmt.Errorf("failed to create zstd encoder: %w", err))
	}
	return encoder
}

func newZstdDecoder() *zstd.Decoder {
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		// cannot fail: the reader takes no options
		panic(fmt.Errorf("failed to create zstd decoder: %w", err))
	}
	return decoder
}

func (g *zstdCompression) Compress(input []byte) ([]byte, error) {
	return zstdEncoder.EncodeAll(input, make([]byte, 0, len(input)/2+64)), nil
}

func (g *zstdCompression) Decompress(input []byte) ([]byte, error) {
	// a nil destination lets the decoder size the output from the frame header
	return zstdDecoder.DecodeAll(input, nil)
}

func (g *zstdCompression) CompressAndWrite(w io.Writer, data []byte) error {
	writer, err := zstd.NewWriter(w, zstd.WithEncoderConcurrency(1), zstd.WithZeroFrames(true))
	if err != nil {
		return err
	}
	if _, err := writer.Write(data); err != nil {
		writer.Close()
		return err
	}
	// Close flushes the final frame, so this is where a writer that only fails on
	// flush (disk full, closed connection) reports itself. The error must not be
	// discarded by a deferred Close, or the frame is silently truncated.
	return writer.Close()
}

func (g *zstdCompression) ReadAndDecompress(r io.Reader) ([]byte, error) {
	reader, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func (g *zstdCompression) String() string {
	return "zstd"
}

var compressionMap = map[string]Compression{
	NoCompression.String():   NoCompression,
	ZstdCompression.String(): ZstdCompression,
}
