package cachefunk

import (
	"bytes"
	"compress/gzip"
	"io"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

type Compression interface {
	Compress(input []byte) ([]byte, error)
	Decompress(input []byte) ([]byte, error)
	CompressAndWrite(w io.Writer, data []byte) error
	ReadAndDecompress(r io.Reader) ([]byte, error)
	String() string
}

type noCompression struct{}

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

type gzipCompression struct{}

var GzipCompression = &gzipCompression{}

func (g *gzipCompression) CompressAndWrite(w io.Writer, data []byte) error {
	gw := gzip.NewWriter(w)
	defer gw.Close()
	_, err := gw.Write(data)
	return err
}

func (g *gzipCompression) ReadAndDecompress(r io.Reader) ([]byte, error) {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer gr.Close()
	return io.ReadAll(gr)
}

func (g *gzipCompression) Compress(input []byte) ([]byte, error) {
	var output bytes.Buffer
	err := g.CompressAndWrite(&output, input)
	if err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func (g *gzipCompression) Decompress(input []byte) ([]byte, error) {
	return g.ReadAndDecompress(bytes.NewReader(input))
}

func (g *gzipCompression) String() string {
	return "gzip"
}

type brotliCompression struct{}

var BrotliCompression = &brotliCompression{}

func (g *brotliCompression) CompressAndWrite(w io.Writer, data []byte) error {
	gw := brotli.NewWriter(w)
	defer gw.Close()
	_, err := gw.Write(data)
	return err
}

func (g *brotliCompression) ReadAndDecompress(r io.Reader) ([]byte, error) {
	gr := brotli.NewReader(r)
	return io.ReadAll(gr)
}

func (g *brotliCompression) Compress(input []byte) ([]byte, error) {
	var output bytes.Buffer
	err := g.CompressAndWrite(&output, input)
	if err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func (g *brotliCompression) Decompress(input []byte) ([]byte, error) {
	return g.ReadAndDecompress(bytes.NewReader(input))
}

func (g *brotliCompression) String() string {
	return "brotli"
}

type zstdCompression struct{}

var ZstdCompression = &zstdCompression{}

func (g *zstdCompression) CompressAndWrite(w io.Writer, data []byte) error {
	gw, err := zstd.NewWriter(w)
	if err != nil {
		return err
	}
	defer gw.Close()
	_, err = gw.Write(data)
	return err
}

func (g *zstdCompression) ReadAndDecompress(r io.Reader) ([]byte, error) {
	gr, err := zstd.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer gr.Close()
	return io.ReadAll(gr)
}

func (g *zstdCompression) Compress(input []byte) ([]byte, error) {
	var output bytes.Buffer
	err := g.CompressAndWrite(&output, input)
	if err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func (g *zstdCompression) Decompress(input []byte) ([]byte, error) {
	return g.ReadAndDecompress(bytes.NewReader(input))
}

func (g *zstdCompression) String() string {
	return "zstd"
}

var compressionMap = map[string]Compression{
	NoCompression.String():     NoCompression,
	GzipCompression.String():   GzipCompression,
	BrotliCompression.String(): BrotliCompression,
	ZstdCompression.String():   ZstdCompression,
}
