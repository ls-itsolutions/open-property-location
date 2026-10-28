package outline

import (
	"slices"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	rings := []Ring{
		{{8.8, 53.0}, {8.8012345, 53.0}, {8.8012345, 53.0098765}, {8.8, 53.0}},
		{{-0.1, -51.5}, {-0.2, -51.5}, {-0.1, -51.6}, {-0.1, -51.5}},
	}
	got, err := Decode(Encode(rings))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.EqualFunc(got, rings, func(a, b Ring) bool { return slices.Equal(a, b) }) {
		t.Errorf("got %v, want %v", got, rings)
	}
	if empty, err := Decode(nil); err != nil || len(empty) != 0 {
		t.Errorf("Decode(nil) = %v, %v", empty, err)
	}
}

func TestDecodeCorrupt(t *testing.T) {
	buf := Encode([]Ring{{{8.8, 53.0}, {8.9, 53.0}, {8.9, 53.1}, {8.8, 53.0}}})
	for _, bad := range [][]byte{buf[:len(buf)-1], {0xff}, {0x7f, 0x01}} {
		if _, err := Decode(bad); err == nil {
			t.Errorf("Decode(%x): expected error", bad)
		}
	}
}
