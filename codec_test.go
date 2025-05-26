package cachefunk_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/rohfle/cachefunk"
)

func TestBodyCodecs(t *testing.T) {
	var codecs = []cachefunk.BodyCodec{
		cachefunk.JSONCodec,
		cachefunk.MsgPackCodec,
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
