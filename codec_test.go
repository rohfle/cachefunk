package cachefunk

import (
	"reflect"
	"testing"
	"time"
)

func TestBodyCodecs(t *testing.T) {
	var codecs = []BodyCodec{
		JSONCodec,
		MsgPackCodec,
	}

	type AAA struct {
		Foo string
		Bar int
		C   *int
		D   time.Time
	}

	var target = AAA{Foo: "hello", Bar: 1, C: nil, D: time.Time{}}

	for _, codec := range codecs {

		data, err := codec.Marshal(target)
		if err != nil {
			t.Fatalf("%T got error when calling Marshal: %s", codec, err)
		}

		var receive AAA
		err = codec.Unmarshal(data, &receive)
		if err != nil {
			t.Fatalf("%T got error when calling Unmarshal: %s", codec, err)
		}

		if !reflect.DeepEqual(receive, target) {
			t.Fatalf("%T marshal to unmarshal delivers inconsistent result: %+v vs %+v", codec, target, receive)
		}
	}

}

func TestStringCodec(t *testing.T) {
	// round trip a string target
	data, err := StringCodec.Marshal("hello")
	if err != nil {
		t.Fatalf("Marshal(string) returned error: %s", err)
	}
	if string(data) != "hello" {
		t.Fatalf("Marshal(string) = %q, expected %q", data, "hello")
	}
	var strTarget string
	if err := StringCodec.Unmarshal(data, &strTarget); err != nil {
		t.Fatalf("Unmarshal(*string) returned error: %s", err)
	}
	if strTarget != "hello" {
		t.Fatalf("Unmarshal(*string) = %q, expected %q", strTarget, "hello")
	}

	// round trip a []byte target
	data, err = StringCodec.Marshal([]byte("world"))
	if err != nil {
		t.Fatalf("Marshal([]byte) returned error: %s", err)
	}
	if string(data) != "world" {
		t.Fatalf("Marshal([]byte) = %q, expected %q", data, "world")
	}
	var byteTarget []byte
	if err := StringCodec.Unmarshal(data, &byteTarget); err != nil {
		t.Fatalf("Unmarshal(*[]byte) returned error: %s", err)
	}
	if string(byteTarget) != "world" {
		t.Fatalf("Unmarshal(*[]byte) = %q, expected %q", byteTarget, "world")
	}

	// an empty but non-nil []byte must stay non-nil: backends such as GORM store
	// the value in a NOT NULL column
	if data, err := StringCodec.Marshal([]byte{}); err != nil || data == nil {
		t.Fatalf("Marshal(empty []byte) = %v, err=%v; expected non-nil empty slice", data, err)
	}

	// unsupported types must error
	if _, err := StringCodec.Marshal(42); err == nil {
		t.Fatal("Marshal(int) expected error but got none")
	}
	if err := StringCodec.Unmarshal([]byte("x"), new(int)); err == nil {
		t.Fatal("Unmarshal(*int) expected error but got none")
	}
	if err := StringCodec.Unmarshal([]byte("x"), strTarget); err == nil {
		t.Fatal("Unmarshal(string) expected error but got none")
	}
}

func TestStringCodecDoesNotAliasCaller(t *testing.T) {
	// Marshal must return bytes owned by the cache: mutating the caller's
	// slice afterwards must not change already marshalled data.
	src := []byte("original")
	data, err := StringCodec.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal([]byte) returned error: %s", err)
	}
	src[0] = 'X'
	if string(data) != "original" {
		t.Fatalf("Marshal aliased caller's slice: got %q, expected %q", data, "original")
	}

	// Unmarshal into *[]byte must copy: mutating the source bytes afterwards
	// must not change the destination.
	src2 := []byte("copy me")
	var dst []byte
	if err := StringCodec.Unmarshal(src2, &dst); err != nil {
		t.Fatalf("Unmarshal(*[]byte) returned error: %s", err)
	}
	src2[0] = 'X'
	if string(dst) != "copy me" {
		t.Fatalf("Unmarshal aliased source bytes: got %q, expected %q", dst, "copy me")
	}
}
