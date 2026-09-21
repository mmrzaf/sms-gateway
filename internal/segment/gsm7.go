package segment

// gsm7Basic is the GSM 03.38 default alphabet. Each character is one septet.
var gsm7Basic = runeSet(
	"@£$¥èéùìòÇ\nØø\rÅå" +
		"Δ_ΦΓΛΩΠΨΣΘΞÆæßÉ " +
		"!\"#¤%&'()*+,-./" +
		"0123456789:;<=>?" +
		"¡ABCDEFGHIJKLMNOPQRSTUVWXYZÄÖÑÜ§" +
		"¿abcdefghijklmnopqrstuvwxyzäöñüà")

// gsm7Extension is the GSM 03.38 extension table. Each character is sent as
// an escape septet followed by the character, so it occupies two septets.
var gsm7Extension = runeSet("\f^{}\\[~]|€")

func runeSet(s string) map[rune]bool {
	m := make(map[rune]bool, len(s))
	for _, r := range s {
		m[r] = true
	}
	return m
}
