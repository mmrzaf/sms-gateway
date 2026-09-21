package segment

import (
	"strings"
	"testing"
)

func TestCount(t *testing.T) {
	persian := func(n int) string { return strings.Repeat("س", n) }
	tests := []struct {
		name     string
		text     string
		encoding Encoding
		units    int
		segments int
	}{
		{"empty", "", GSM7, 0, 0},
		{"short ascii", "Hello", GSM7, 5, 1},
		{"single segment limit", strings.Repeat("a", 160), GSM7, 160, 1},
		{"one over single", strings.Repeat("a", 161), GSM7, 161, 2},
		{"two full parts", strings.Repeat("a", 306), GSM7, 306, 2},
		{"one over two parts", strings.Repeat("a", 307), GSM7, 307, 3},
		{"extension chars fill one segment", strings.Repeat("€", 80), GSM7, 160, 1},
		{"escape not split across parts", strings.Repeat("a", 152) + "€" + strings.Repeat("a", 152), GSM7, 306, 3},
		{"gsm7 accented and greek", "Ça coûte ΔΣ", UCS2, 11, 1},
		{"gsm7 accented only", "Ça fait ΔΣ", GSM7, 10, 1},
		{"newline and carriage return", "a\r\nb", GSM7, 4, 1},
		{"persian single limit", persian(70), UCS2, 70, 1},
		{"persian one over", persian(71), UCS2, 71, 2},
		{"persian two parts", persian(134), UCS2, 134, 2},
		{"persian one over two parts", persian(135), UCS2, 135, 3},
		{"emoji is two units", "Hi 😀", UCS2, 5, 1},
		{"surrogate pair starts next part", strings.Repeat("x", 66) + "😀" + "ééééé", UCS2, 73, 2},
		{"surrogate pair at part boundary", strings.Repeat("ب", 66) + "😀" + strings.Repeat("ب", 66), UCS2, 134, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Count(tc.text)
			want := Result{Encoding: tc.encoding, Units: tc.units, Segments: tc.segments}
			if got != want {
				t.Errorf("Count(%q) = %+v, want %+v", tc.text, got, want)
			}
		})
	}
}

func TestMaximumLengths(t *testing.T) {
	if got := Count(strings.Repeat("a", 10*GSM7Part)).Segments; got != 10 {
		t.Errorf("1530 septets: %d segments, want 10", got)
	}
	if got := Count(strings.Repeat("a", 10*GSM7Part+1)).Segments; got != 11 {
		t.Errorf("1531 septets: %d segments, want 11", got)
	}
	if got := Count(strings.Repeat("ک", 10*UCS2Part)).Segments; got != 10 {
		t.Errorf("670 units: %d segments, want 10", got)
	}
}

func TestDetectCoversWholeAlphabet(t *testing.T) {
	for r := range gsm7Basic {
		if Detect(string(r)) != GSM7 {
			t.Errorf("%q should be GSM-7", r)
		}
	}
	for r := range gsm7Extension {
		if res := Count(string(r)); res.Encoding != GSM7 || res.Units != 2 {
			t.Errorf("%q: got %+v, want GSM-7 with 2 units", r, res)
		}
	}
	if len(gsm7Basic) != 127 {
		// 128 code points, minus the escape character that introduces the extension table.
		t.Errorf("basic alphabet has %d characters, want 127", len(gsm7Basic))
	}
}
