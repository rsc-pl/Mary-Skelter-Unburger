package mskit

// Native, deterministic LZO1X matching encoder for changed ENC rows.
// Unlike CompressLZO1XLiterals, it emits back-references and is intended to
// avoid inflating the enclosing BILZ stream when the original used LZO1X.
// The emitted M3 form is restricted to 16 KiB backward distances, and every
// output is independently checked by DecompressLZO1X before being returned.
import (
	"bytes"
	"encoding/binary"
	"fmt"
)

const lzoMatchWindow = 1 << 14

func appendLZOExtended(out []byte, n int) []byte {
	// The decoder's extendLength(base) adds 255 for each 0 and then the
	// terminating positive byte; n must be at least 1.
	for n > 255 {
		out = append(out, 0)
		n -= 255
	}
	return append(out, byte(n))
}

func appendLZOLiterals(out []byte, data []byte, initial bool) ([]byte, error) {
	n := len(data)
	if n < 4 {
		return nil, fmt.Errorf("literal run %d too short", n)
	}
	if initial && n <= 238 {
		out = append(out, byte(17+n))
	} else if n <= 18 {
		out = append(out, byte(n-3))
	} else {
		out = append(out, 0)
		out = appendLZOExtended(out, n-18)
	}
	return append(out, data...), nil
}

func appendLZOM3(out []byte, matchLen, distance int) ([]byte, int, error) {
	if matchLen < 4 || distance < 1 || distance > lzoMatchWindow {
		return nil, 0, fmt.Errorf("unsupported LZO M3 length=%d distance=%d", matchLen, distance)
	}
	if matchLen <= 33 {
		out = append(out, byte(32+matchLen-2))
	} else {
		out = append(out, 32)
		out = appendLZOExtended(out, matchLen-33)
	}
	b0index := len(out)
	d := distance - 1
	out = append(out, byte((d&63)<<2), byte(d>>6))
	return out, b0index, nil
}

func CompressLZO1XMatches(data []byte) ([]byte, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("LZO row shorter than four bytes")
	}
	if len(data) > 1<<27 {
		return nil, fmt.Errorf("LZO row too large")
	}
	// Keep four previous positions per four-byte key. A bounded candidate
	// search limits CPU cost, even for pathologically repetitive rows.
	type candidates struct {
		pos   [4]int
		count int
	}
	dict := make(map[uint32]candidates)
	add := func(i int) {
		if i+4 > len(data) {
			return
		}
		key := binary.LittleEndian.Uint32(data[i:])
		c := dict[key]
		c.pos[c.count&3] = i
		c.count++
		dict[key] = c
	}
	for i := 0; i < 4 && i+4 <= len(data); i++ {
		add(i)
	}
	out := make([]byte, 0, len(data)/2+16)
	literalStart := 0
	previousMatchB0 := -1
	matched := false
	for i := 4; i+4 <= len(data); {
		key := binary.LittleEndian.Uint32(data[i:])
		c := dict[key]
		bestLength, bestDistance := 0, 0
		limit := c.count
		if limit > 4 {
			limit = 4
		}
		for k := 0; k < limit; k++ {
			pos := c.pos[(c.count-1-k)&3]
			dist := i - pos
			if dist <= 0 || dist > lzoMatchWindow {
				continue
			}
			j := 4
			for i+j < len(data) && data[pos+j] == data[i+j] {
				j++
			}
			if j > bestLength {
				bestLength, bestDistance = j, dist
			}
		}
		if bestLength < 4 {
			add(i)
			i++
			continue
		}
		literals := data[literalStart:i]
		if !matched {
			if len(literals) < 4 {
				return nil, fmt.Errorf("internal initial literal run too short")
			}
			var err error
			out, err = appendLZOLiterals(out, literals, true)
			if err != nil {
				return nil, err
			}
		} else if len(literals) <= 3 {
			// In LZO1X the low two bits of M3's first offset byte encode
			// up to three literal bytes between successive match tokens.
			out[previousMatchB0] |= byte(len(literals))
			out = append(out, literals...)
		} else {
			var err error
			out, err = appendLZOLiterals(out, literals, false)
			if err != nil {
				return nil, err
			}
		}
		var err error
		out, previousMatchB0, err = appendLZOM3(out, bestLength, bestDistance)
		if err != nil {
			return nil, err
		}
		matched = true
		end := i + bestLength
		for ; i < end; i++ {
			add(i)
		}
		literalStart = end
	}
	if !matched {
		return CompressLZO1XLiterals(data)
	}
	suffix := data[literalStart:]
	if len(suffix) <= 3 {
		out[previousMatchB0] |= byte(len(suffix))
		out = append(out, suffix...)
	} else {
		var err error
		out, err = appendLZOLiterals(out, suffix, false)
		if err != nil {
			return nil, err
		}
	}
	out = append(out, 0x11, 0, 0)
	check, err := DecompressLZO1X(out, len(data))
	if err != nil || !bytes.Equal(check, data) {
		return nil, fmt.Errorf("native matching LZO1X encode roundtrip failed: %w", err)
	}
	// A short or high-entropy changed row can compress worse with matches.
	// Always choose the smaller valid encoding, never expand unnecessarily.
	literal, err := CompressLZO1XLiterals(data)
	if err != nil {
		return nil, err
	}
	if len(literal) < len(out) {
		return literal, nil
	}
	return out, nil
}
