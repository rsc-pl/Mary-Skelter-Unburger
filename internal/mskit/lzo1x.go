package mskit

import "fmt"

// DecompressLZO1X decodes the LZO1X stream format used by compressed ENC rows.
// It implements the safe decompression state machine from the reference LZO1X
// decoder, but performs all bounds checks in Go and does not require liblzo2.
func DecompressLZO1X(src []byte, expectedSize int) ([]byte, error) {
	if expectedSize < 0 {
		return nil, fmt.Errorf("invalid LZO expected size %d", expectedSize)
	}
	out := make([]byte, 0, expectedSize)
	ip := 0

	needIn := func(n int) error {
		if n < 0 || ip < 0 || ip+n > len(src) {
			return fmt.Errorf("truncated LZO stream at input %d (need %d bytes)", ip, n)
		}
		return nil
	}
	copyLiterals := func(n int) error {
		if n < 0 {
			return fmt.Errorf("negative LZO literal length %d", n)
		}
		if err := needIn(n); err != nil {
			return err
		}
		if expectedSize >= 0 && len(out)+n > expectedSize {
			return fmt.Errorf("LZO output exceeds expected size %d", expectedSize)
		}
		out = append(out, src[ip:ip+n]...)
		ip += n
		return nil
	}
	copyMatch := func(pos, n int) error {
		if n < 0 {
			return fmt.Errorf("negative LZO match length %d", n)
		}
		if pos < 0 || pos >= len(out) {
			return fmt.Errorf("invalid LZO match offset %d at output %d", pos, len(out))
		}
		if expectedSize >= 0 && len(out)+n > expectedSize {
			return fmt.Errorf("LZO output exceeds expected size %d", expectedSize)
		}
		// LZO matches may overlap, so copy one byte at a time from the growing
		// output buffer rather than slicing once.
		for i := 0; i < n; i++ {
			idx := pos + i
			if idx < 0 || idx >= len(out) {
				return fmt.Errorf("invalid overlapping LZO match read %d at output %d", idx, len(out))
			}
			out = append(out, out[idx])
		}
		return nil
	}
	extendLength := func(base int) (int, error) {
		n := 0
		for {
			if err := needIn(1); err != nil {
				return 0, err
			}
			b := int(src[ip])
			ip++
			if b != 0 {
				return n + base + b, nil
			}
			n += 255
		}
	}

	if len(src) == 0 {
		if expectedSize == 0 {
			return []byte{}, nil
		}
		return nil, fmt.Errorf("empty LZO stream")
	}

	readByte := func() (int, error) {
		if err := needIn(1); err != nil {
			return 0, err
		}
		v := int(src[ip])
		ip++
		return v, nil
	}

	t, err := readByte()
	if err != nil {
		return nil, err
	}
	if t > 17 {
		t -= 17
		if t < 4 {
			// Initial short literal run falls into match_next semantics below.
			if err := copyLiterals(t); err != nil {
				return nil, err
			}
			t, err = readByte()
			if err != nil {
				return nil, err
			}
		} else {
			if err := copyLiterals(t); err != nil {
				return nil, err
			}
			t, err = readByte()
			if err != nil {
				return nil, err
			}
			if t < 16 {
				// first_literal_run: a 3-byte match using the short M2 form.
				b, err := readByte()
				if err != nil {
					return nil, err
				}
				mpos := len(out) - (1 + 0x800) - (t >> 2) - (b << 2)
				if err := copyMatch(mpos, 3); err != nil {
					return nil, err
				}
				trail := t & 3
				if trail != 0 {
					if err := copyLiterals(trail); err != nil {
						return nil, err
					}
					t, err = readByte()
					if err != nil {
						return nil, err
					}
				} else {
					t = -1 // force a fresh token at the top of the loop
				}
			}
		}
	}

	for {
		if t < 0 {
			t, err = readByte()
			if err != nil {
				return nil, err
			}
		}

		if t < 16 {
			if t == 0 {
				t, err = extendLength(15)
				if err != nil {
					return nil, err
				}
			}
			if err := copyLiterals(t + 3); err != nil {
				return nil, err
			}
			t, err = readByte()
			if err != nil {
				return nil, err
			}
			if t < 16 {
				b, err := readByte()
				if err != nil {
					return nil, err
				}
				mpos := len(out) - (1 + 0x800) - (t >> 2) - (b << 2)
				if err := copyMatch(mpos, 3); err != nil {
					return nil, err
				}
				trail := t & 3
				if trail == 0 {
					t = -1
					continue
				}
				if err := copyLiterals(trail); err != nil {
					return nil, err
				}
				t, err = readByte()
				if err != nil {
					return nil, err
				}
			}
		}

		for t >= 16 {
			var mpos, mlen, trail int
			switch {
			case t >= 64:
				b, err := readByte()
				if err != nil {
					return nil, err
				}
				mpos = len(out) - 1 - ((t >> 2) & 7) - (b << 3)
				mlen = (t >> 5) + 1
				trail = t & 3

			case t >= 32:
				n := t & 31
				if n == 0 {
					n, err = extendLength(31)
					if err != nil {
						return nil, err
					}
				}
				if err := needIn(2); err != nil {
					return nil, err
				}
				b0, b1 := int(src[ip]), int(src[ip+1])
				ip += 2
				mpos = len(out) - 1 - (b0 >> 2) - (b1 << 6)
				mlen = n + 2
				trail = b0 & 3

			case t >= 16:
				n := t & 7
				mpos = len(out) - ((t & 8) << 11)
				if n == 0 {
					n, err = extendLength(7)
					if err != nil {
						return nil, err
					}
				}
				if err := needIn(2); err != nil {
					return nil, err
				}
				b0, b1 := int(src[ip]), int(src[ip+1])
				ip += 2
				mpos -= (b0 >> 2) + (b1 << 6)
				trail = b0 & 3
				if mpos == len(out) {
					if len(out) != expectedSize {
						return nil, fmt.Errorf("LZO end marker at output %d, expected %d", len(out), expectedSize)
					}
					if ip != len(src) {
						return nil, fmt.Errorf("LZO stream has %d trailing bytes", len(src)-ip)
					}
					return out, nil
				}
				mpos -= 0x4000
				mlen = n + 2
			}

			if err := copyMatch(mpos, mlen); err != nil {
				return nil, err
			}
			if trail == 0 {
				t = -1
				break
			}
			if err := copyLiterals(trail); err != nil {
				return nil, err
			}
			t, err = readByte()
			if err != nil {
				return nil, err
			}
		}
	}
}
