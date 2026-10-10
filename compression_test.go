package cachefunk

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// failWriter fails once more than limit bytes have been written to it. Codecs
// that buffer small payloads only report a failing destination during Close, so
// this is how a write failure is made to happen at flush time.
type failWriter struct {
	written int
	limit   int
}

func (f *failWriter) Write(p []byte) (int, error) {
	if f.written+len(p) > f.limit {
		return 0, errors.New("destination is full")
	}
	f.written += len(p)
	return len(p), nil
}

func compressionPayloads() map[string][]byte {
	var varied bytes.Buffer
	for i := range 256 {
		varied.WriteString("record-")
		varied.WriteByte(byte('a' + i%26))
		varied.WriteString(`{"id":1234,"name":"some record value"}`)
	}
	return map[string][]byte{
		"empty":  {},
		"small":  []byte("hello world"),
		"varied": varied.Bytes(),
	}
}

func TestCompressionRoundTrip(t *testing.T) {
	for name, compression := range compressionMap {
		for payloadName, payload := range compressionPayloads() {
			compressed, err := compression.Compress(payload)
			if err != nil {
				t.Fatalf("%s: Compress(%s) returned an error: %s", name, payloadName, err)
			}
			// a compression that frames its output must still produce a stored
			// value for an empty payload, otherwise an empty cached value is
			// indistinguishable from a missing entry (NoCompression stores it
			// as-is, so it stays empty)
			if name != NoCompression.String() && len(compressed) == 0 {
				t.Errorf("%s: Compress(%s) produced no bytes", name, payloadName)
			}
			decompressed, err := compression.Decompress(compressed)
			if err != nil {
				t.Fatalf("%s: Decompress(%s) returned an error: %s", name, payloadName, err)
			}
			if !bytes.Equal(decompressed, payload) {
				t.Errorf("%s: round trip of %s changed the payload: %d bytes in, %d bytes out",
					name, payloadName, len(payload), len(decompressed))
			}
		}

		// corrupt input must fail loudly rather than yielding a short value
		if _, err := compression.Decompress([]byte("this is not compressed data")); err == nil && name != NoCompression.String() {
			t.Errorf("%s: Decompress accepted corrupt input", name)
		}
	}
}

func TestCompressionStreaming(t *testing.T) {
	for name, compression := range compressionMap {
		for payloadName, payload := range compressionPayloads() {
			var output bytes.Buffer
			if err := compression.CompressAndWrite(&output, payload); err != nil {
				t.Fatalf("%s: CompressAndWrite(%s) returned an error: %s", name, payloadName, err)
			}
			decompressed, err := compression.ReadAndDecompress(bytes.NewReader(output.Bytes()))
			if err != nil {
				t.Fatalf("%s: ReadAndDecompress(%s) returned an error: %s", name, payloadName, err)
			}
			if !bytes.Equal(decompressed, payload) {
				t.Errorf("%s: streaming round trip of %s changed the payload: %d bytes in, %d bytes out",
					name, payloadName, len(payload), len(decompressed))
			}
		}
	}
}

// TestCompressionStreamingFlushError checks that a destination which only fails
// when the codec flushes its final frame is reported to the caller. The bytes
// are buffered until Close, so returning only the Write error silently produces
// a truncated stream.
func TestCompressionStreamingFlushError(t *testing.T) {
	payload := bytes.Repeat([]byte("some cached payload "), 64)

	for name, compression := range compressionMap {
		// allow a couple of header bytes through, then fail
		destination := &failWriter{limit: 4}
		err := compression.CompressAndWrite(destination, payload)
		if err == nil {
			t.Errorf("%s: CompressAndWrite reported success after writing only %d of %d bytes",
				name, destination.written, len(payload))
			continue
		}
		t.Logf("%s: correctly reported %v after %d bytes", name, err, destination.written)
	}
}

// TestCompressionAlreadyCompressed checks NoCompression passes bytes through unchanged
func TestCompressionAlreadyCompressed(t *testing.T) {
	payload := []byte{0x1f, 0x8b, 0x08, 0x00, 0xde, 0xad, 0xbe, 0xef} // gzip magic + header bytes

	compressed, err := NoCompression.Compress(payload)
	if err != nil {
		t.Fatalf("NoCompression.Compress returned an error: %s", err)
	}
	if !bytes.Equal(compressed, payload) {
		t.Errorf("NoCompression.Compress changed the payload")
	}
	decompressed, err := NoCompression.Decompress(compressed)
	if err != nil {
		t.Fatalf("NoCompression.Decompress returned an error: %s", err)
	}
	if !bytes.Equal(decompressed, payload) {
		t.Errorf("NoCompression.Decompress changed the payload")
	}

	var output bytes.Buffer
	if err := NoCompression.CompressAndWrite(&output, payload); err != nil {
		t.Fatalf("NoCompression.CompressAndWrite returned an error: %s", err)
	}
	if _, err := io.Copy(io.Discard, bytes.NewReader(output.Bytes())); err != nil {
		t.Fatalf("unexpected copy error: %s", err)
	}
}
