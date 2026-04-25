package kairos

import "fmt"

// Crockford's Base32 alphabet (excludes I, L, O, U to avoid ambiguity).
const crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// crockfordDecode maps ASCII bytes to 5-bit values. -1 = invalid.
var crockfordDecode [256]int8

func init() {
	for i := range crockfordDecode {
		crockfordDecode[i] = -1
	}
	for i, c := range crockfordAlphabet {
		crockfordDecode[c] = int8(i)
		// Accept lowercase
		if c >= 'A' && c <= 'Z' {
			crockfordDecode[c+32] = int8(i)
		}
	}
	// Crockford specifies these common substitutions
	crockfordDecode['i'] = 1  // I → 1
	crockfordDecode['I'] = 1
	crockfordDecode['l'] = 1  // L → 1
	crockfordDecode['L'] = 1
	crockfordDecode['o'] = 0  // O → 0
	crockfordDecode['O'] = 0
}

// encode converts 10 bytes (80 bits, top 5 zero) into a 15-char Crockford Base32 string.
// Only the low 75 bits are meaningful; the top 5 bits must be zero.
func encode(buf [10]byte) string {
	var out [15]byte
	// Extract 5-bit groups from 75 bits (big-endian).
	// We have 80 bits in buf. Skip the top 5 bits by starting extraction at bit 5.
	// Bit position i (0-based from MSB) maps to:
	//   byte index: i/8, bit within byte: 7-(i%8)

	// Simpler: collect all 80 bits into a big-endian stream and extract 15 groups of 5.
	// Since top 5 bits are zero, first char encodes bits 0-4 (all zero → '0').
	// But that wastes a char. We skip the top 5 bits:

	// Actually, we DO encode all 15 chars from the 75 meaningful bits.
	// 15 * 5 = 75. The top 5 bits of buf are padding zeros.
	// We extract starting from bit 5.

	// Approach: shift bits out of two uint64 halves.
	hi := uint64(buf[0])<<8 | uint64(buf[1])                  // 16 bits
	lo := uint64(buf[2])<<56 | uint64(buf[3])<<48 |
		uint64(buf[4])<<40 | uint64(buf[5])<<32 |
		uint64(buf[6])<<24 | uint64(buf[7])<<16 |
		uint64(buf[8])<<8 | uint64(buf[9])                     // 64 bits

	// Total 80 bits. We want bits 5-79 (the 75 meaningful bits).
	// Char 0 = bits 5-9, char 1 = bits 10-14, ..., char 14 = bits 70-74.
	//
	// Combine into a single extraction loop using the 80-bit value.
	// For char i, we want bits (5 + i*5) through (5 + i*5 + 4).
	// Shift right by (80 - 5 - i*5 - 5) = (70 - i*5) and mask with 0x1F.

	for i := 0; i < 15; i++ {
		shift := uint(70 - i*5)
		var val uint8
		if shift >= 64 {
			// Bits come from hi
			val = uint8((hi >> (shift - 64)) & 0x1F)
		} else if shift >= 59 {
			// Bits span hi and lo
			val = uint8(((hi << (64 - shift)) | (lo >> shift)) & 0x1F)
		} else {
			// Bits come from lo
			val = uint8((lo >> shift) & 0x1F)
		}
		out[i] = crockfordAlphabet[val]
	}
	return string(out[:])
}

// decode converts a 15-char Crockford Base32 string into 10 bytes (80 bits, top 5 zero).
func decode(s string) ([10]byte, error) {
	var buf [10]byte
	if len(s) != 15 {
		return buf, fmt.Errorf("invalid length %d, want 15", len(s))
	}

	var hi uint64 // top 16 bits of 80-bit value
	var lo uint64 // bottom 64 bits

	for i := 0; i < 15; i++ {
		v := crockfordDecode[s[i]]
		if v < 0 {
			return buf, fmt.Errorf("invalid character %q at position %d", s[i], i)
		}
		val := uint64(v)
		shift := uint(70 - i*5)
		if shift >= 64 {
			hi |= val << (shift - 64)
		} else if shift >= 59 {
			hi |= val >> (64 - shift)
			lo |= val << shift
		} else {
			lo |= val << shift
		}
	}

	buf[0] = byte(hi >> 8)
	buf[1] = byte(hi)
	buf[2] = byte(lo >> 56)
	buf[3] = byte(lo >> 48)
	buf[4] = byte(lo >> 40)
	buf[5] = byte(lo >> 32)
	buf[6] = byte(lo >> 24)
	buf[7] = byte(lo >> 16)
	buf[8] = byte(lo >> 8)
	buf[9] = byte(lo)
	return buf, nil
}
