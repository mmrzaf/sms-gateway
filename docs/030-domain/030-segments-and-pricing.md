# Segments and Pricing

How the gateway chooses an encoding for a message, how many segments the message occupies, and what it costs.

## Encoding selection

A message is encoded as **GSM-7** if every character belongs to the GSM 03.38 basic character set or its extension table. Otherwise it is encoded as **UCS-2**. There is no transliteration or character substitution; the text is transmitted as submitted.

### GSM 03.38 basic character set (1 septet each)

```
@ £ $ ¥ è é ù ì ò Ç <LF> Ø ø <CR> Å å
Δ _ Φ Γ Λ Ω Π Ψ Σ Θ Ξ Æ æ ß É <SP>
! " # ¤ % & ' ( ) * + , - . /
0 1 2 3 4 5 6 7 8 9 : ; < = > ?
¡ A–Z Ä Ö Ñ Ü §
¿ a–z ä ö ñ ü à
```

### GSM 03.38 extension table (2 septets each: escape + character)

```
<FF> ^ { } \ [ ~ ] | €
```

## Segment limits

| Encoding | Unit counted | Single segment | Per segment when multipart |
|---|---|---|---|
| `gsm7` | Septets | 160 | 153 |
| `ucs2` | UTF-16 code units | 70 | 67 |

Multipart segments carry a user data header, which is why each part holds less than a single segment.

**Maximum length.** A message may occupy at most 10 segments (`MAX_SEGMENTS`): 1,530 septets in GSM-7 or 670 UTF-16 code units in UCS-2. Longer messages are rejected with `422` and field error `text_too_long`.

## Counting algorithm

1. Compute the total length in the encoding's unit: septets for GSM-7 (extension characters count 2), UTF-16 code units for UCS-2 (characters outside the Basic Multilingual Plane, such as most emoji, count 2).
2. If the total fits a single segment, the message has 1 segment.
3. Otherwise, fill segments greedily with the per-part capacity, **never splitting** a GSM-7 escape sequence or a UTF-16 surrogate pair across two segments. A character that does not fit in the remaining space of a segment starts the next segment.

Because of rule 3, the segment count can exceed `ceil(length / per-part capacity)` by one when a two-unit character falls on a boundary.

## Examples

| Text | Encoding | Length | Segments |
|---|---|---|---|
| `Hello` | gsm7 | 5 septets | 1 |
| 160 × `a` | gsm7 | 160 septets | 1 |
| 161 × `a` | gsm7 | 161 septets | 2 |
| 306 × `a` | gsm7 | 306 septets | 2 |
| 307 × `a` | gsm7 | 307 septets | 3 |
| 80 × `€` | gsm7 | 160 septets | 1 |
| 152 × `a`, `€`, 152 × `a` | gsm7 | 306 septets | 3 (see note) |
| 70 Persian characters | ucs2 | 70 units | 1 |
| 71 Persian characters | ucs2 | 71 units | 2 |
| 134 Persian characters | ucs2 | 134 units | 2 |
| 135 Persian characters | ucs2 | 135 units | 3 |
| `Hi 😀` | ucs2 | 5 units | 1 |

Note on the `€` example: 306 septets would fit exactly into two 153-septet parts, but part 1 holds the first 152 `a` characters and has 1 septet left, which cannot hold the 2-septet `€`. The `€` starts part 2, which then holds `€` plus 151 `a` characters (153 septets), and the last `a` starts part 3. Result: 3 segments.

## Cost

```
cost = segments × price(type)
```

| Message | Segments | Type | Cost |
|---|---|---|---|
| `Your code is 482913` | 1 | normal | 1 |
| `Your code is 482913` | 1 | express | 3 |
| 200-character English text | 2 | normal | 2 |
| 100-character Persian text | 2 | express | 6 |

The encoding, segment count, and cost are returned in the accept response and stored on the message.

## Validation

| Condition | Result |
|---|---|
| Text empty | `422`, field `text`, code `required` |
| More than `MAX_SEGMENTS` segments | `422`, field `text`, code `text_too_long` |
| Text contains a NUL character or invalid UTF-8 | `422`, field `text`, code `invalid_text` |

## Related

- [Credits and billing](020-credits-and-billing.md)
- [Customer API](../050-api/020-customer-api.md)
