// Package segment determines how a text message is encoded for SMS
// transmission and how many segments it occupies.
//
// A message is sent in GSM-7 when every character belongs to the GSM 03.38
// alphabet or its extension table, and in UCS-2 otherwise. A message that
// does not fit one segment is split into parts that each carry a header, so
// multipart segments hold fewer units than a single segment.
package segment

// Encoding is the character encoding used on the air interface.
type Encoding string

// Supported encodings.
const (
	GSM7 Encoding = "gsm7"
	UCS2 Encoding = "ucs2"
)

// Segment capacities, in septets for GSM-7 and UTF-16 code units for UCS-2.
const (
	GSM7Single = 160
	GSM7Part   = 153
	UCS2Single = 70
	UCS2Part   = 67
)

// Result describes an encoded message.
type Result struct {
	Encoding Encoding
	// Units is the length in septets (GSM-7) or UTF-16 code units (UCS-2).
	Units int
	// Segments is the number of SMS segments; zero for an empty text.
	Segments int
}

// Count detects the encoding of text and counts its segments.
func Count(text string) Result {
	enc := Detect(text)
	single, part := GSM7Single, GSM7Part
	if enc == UCS2 {
		single, part = UCS2Single, UCS2Part
	}

	total := 0
	for _, r := range text {
		total += width(enc, r)
	}
	if total == 0 {
		return Result{Encoding: enc}
	}
	if total <= single {
		return Result{Encoding: enc, Units: total, Segments: 1}
	}

	// Fill parts greedily. A two-unit character (a GSM-7 escape sequence or
	// a UTF-16 surrogate pair) is never split across parts: if it does not
	// fit in the space left, it starts the next part.
	segments, used := 1, 0
	for _, r := range text {
		w := width(enc, r)
		if used+w > part {
			segments++
			used = 0
		}
		used += w
	}
	return Result{Encoding: enc, Units: total, Segments: segments}
}

// Detect returns GSM7 if every character of text can be sent in GSM-7,
// and UCS2 otherwise.
func Detect(text string) Encoding {
	for _, r := range text {
		if !gsm7Basic[r] && !gsm7Extension[r] {
			return UCS2
		}
	}
	return GSM7
}

// width is the number of units r occupies in enc.
func width(enc Encoding, r rune) int {
	if enc == GSM7 {
		if gsm7Extension[r] {
			return 2
		}
		return 1
	}
	if r > 0xFFFF {
		return 2 // surrogate pair
	}
	return 1
}
