package graphicsgo

// — research-only CFF Blue-zone preparation/capture in signed 16.16.
// This code is deliberately not wired into game graphics or the glyph hinter.
// It implements a bounded source/zone capture subcase, NOT all of FreeType's
// cf2_blues_init/cf2_hintmap_build (notably no LanguageGroup em-box hints).

import "math"

type CFFBlueZoneFixedExperimental struct {
	BottomCS, TopCS, FlatCS, FlatDS int64
	BottomZone                      bool
}
type CFFBlueStateFixedExperimental struct {
	Zones                      []CFFBlueZoneFixedExperimental
	BlueShift, BlueFuzz, Boost int64
	SuppressOvershoot          bool
}

func cffBlueFixed(v float64) (int64, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < -32768 || v > 32767 {
		return 0, fontErr("CFF Blue fixed source bounds")
	}
	x := math.Round(v * 65536)
	if math.Abs(v*65536-x) > 1e-7 || x < math.MinInt32 || x > math.MaxInt32 {
		return 0, fontErr("CFF Blue nonrepresentable fixed value")
	}
	return int64(x), nil
}
func cffBlueRound(v int64) (int64, error) {
	if v < math.MinInt32 || v > math.MaxInt32 {
		return 0, fontErr("CFF Blue round bounds")
	}
	r := ((v + 0x8000) >> 16) << 16
	if r < math.MinInt32 || r > math.MaxInt32 {
		return 0, fontErr("CFF Blue rounded overflow")
	}
	return r, nil
}

// Private DICT BlueScale is commonly a BCD decimal, which cannot be
// represented exactly in 16.16. Quantize it like a CF2_Fixed number instead
// of demanding exact source fixed representation from this one metric.
func cffBlueMetricFixed(v float64) (int64, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 || v > 32767 {
		return 0, fontErr("CFF BlueScale invalid")
	}
	n := int64(math.Round(v * 65536))
	if n <= 0 || n > math.MaxInt32 {
		return 0, fontErr("CFF BlueScale not representable")
	}
	return n, nil
}
func cffBlueAbs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// PrepareCFFBlueZonesFixedExperimental builds normal CFF BlueValues and
// OtherBlues zones, applying family flat-edge adjustment, BlueScale cutoff,
// overshoot suppression, and boost. The caller must rule out LanguageGroup 1
// synthetic em-box hint mode; that mode is not represented in CFFHintPrivate.
// Hint origin must be zero in this bounded version.
func PrepareCFFBlueZonesFixedExperimental(p CFFHintPrivate, scale, darkenY int64, stemDarkened bool) (CFFBlueStateFixedExperimental, error) {
	var s CFFBlueStateFixedExperimental
	if scale <= 0 || scale > math.MaxInt32 || darkenY < 0 || darkenY > math.MaxInt32/4 || len(p.BlueValues) > 14 || len(p.OtherBlues) > 10 || len(p.FamilyBlues) > 14 || len(p.FamilyOtherBlues) > 10 {
		return s, fontErr("CFF Blue invalid parameters")
	}
	for _, a := range [][]float64{p.BlueValues, p.OtherBlues, p.FamilyBlues, p.FamilyOtherBlues} {
		if len(a)%2 != 0 {
			return s, fontErr("CFF Blue odd zone array")
		}
	}
	var maxHeight int64
	add := func(a []float64, main bool) error {
		for i := 0; i < len(a); i += 2 {
			lo, e := cffBlueFixed(a[i])
			if e != nil {
				return e
			}
			hi, e := cffBlueFixed(a[i+1])
			if e != nil {
				return e
			}
			if hi < lo {
				continue
			} // FreeType ignores negative zone height.
			if hi-lo > maxHeight {
				maxHeight = hi - lo
			}
			bottom := !main || i == 0
			if !bottom {
				lo += 2 * darkenY
				hi += 2 * darkenY
			}
			if lo < math.MinInt32 || hi > math.MaxInt32 {
				return fontErr("CFF Blue darkening overflow")
			}
			z := CFFBlueZoneFixedExperimental{BottomCS: lo, TopCS: hi, BottomZone: bottom}
			if bottom {
				z.FlatCS = hi
			} else {
				z.FlatCS = lo
			}
			s.Zones = append(s.Zones, z)
		}
		return nil
	}
	if err := add(p.BlueValues, true); err != nil {
		return CFFBlueStateFixedExperimental{}, err
	}
	if err := add(p.OtherBlues, false); err != nil {
		return CFFBlueStateFixedExperimental{}, err
	}
	if len(s.Zones) > 12 {
		return CFFBlueStateFixedExperimental{}, fontErr("CFF Blue zone limit")
	}
	// Family alignment: nearest corresponding flat edge within one device pixel.
	// The comparison is in character space; top family flats include darkening.
	onePx, err := cffFixedDivExperimental(65536, scale)
	if err != nil {
		return CFFBlueStateFixedExperimental{}, err
	}
	for i := range s.Zones {
		z := &s.Zones[i]
		best := onePx
		search := func(arr []float64, start int, top bool) error {
			for k := start; k+1 < len(arr); k += 2 {
				j := k
				if !top {
					j = k + 1
				}
				v, e := cffBlueFixed(arr[j])
				if e != nil {
					return e
				}
				if top {
					v += 2 * darkenY
				}
				if v < math.MinInt32 || v > math.MaxInt32 {
					return fontErr("CFF Blue family overflow")
				}
				d := cffBlueAbs(z.FlatCS - v)
				if d < best {
					best = d
					z.FlatCS = v
				}
			}
			return nil
		}
		if z.BottomZone {
			if e := search(p.FamilyOtherBlues, 0, false); e != nil {
				return CFFBlueStateFixedExperimental{}, e
			}
			if e := search(p.FamilyBlues[:min(2, len(p.FamilyBlues))], 0, false); e != nil {
				return CFFBlueStateFixedExperimental{}, e
			}
		} else if e := search(p.FamilyBlues, 2, true); e != nil {
			return CFFBlueStateFixedExperimental{}, e
		}
	}
	blueScale, err := cffBlueMetricFixed(p.BlueScale)
	if err != nil || blueScale <= 0 {
		return CFFBlueStateFixedExperimental{}, fontErr("CFF BlueScale invalid")
	}
	s.BlueShift, err = cffBlueFixed(p.BlueShift)
	if err != nil || s.BlueShift < 0 {
		return CFFBlueStateFixedExperimental{}, fontErr("CFF BlueShift invalid")
	}
	s.BlueFuzz, err = cffBlueFixed(p.BlueFuzz)
	if err != nil || s.BlueFuzz < 0 {
		return CFFBlueStateFixedExperimental{}, fontErr("CFF BlueFuzz invalid")
	}
	if maxHeight > 0 {
		limit, er := cffFixedDivExperimental(65536, maxHeight)
		if er != nil {
			return CFFBlueStateFixedExperimental{}, er
		}
		if blueScale > limit {
			blueScale = limit
		}
	}
	if scale < blueScale {
		s.SuppressOvershoot = true
		// Both terms are signed 16.16. The fractional 0.6 is a documented
		// FreeType CFF threshold; cap strictly below half a device pixel.
		const sixTenths = int64(39322)
		boost := (sixTenths*(blueScale-scale) + blueScale/2) / blueScale
		if boost > 0x7fff {
			boost = 0x7fff
		}
		if stemDarkened {
			boost = 0
		}
		s.Boost = boost
	}
	for i := range s.Zones {
		z := &s.Zones[i]
		x, e := cffFixedMulExperimental(z.FlatCS, scale)
		if e != nil {
			return CFFBlueStateFixedExperimental{}, e
		}
		if z.BottomZone {
			x -= s.Boost
		} else {
			x += s.Boost
		}
		z.FlatDS, e = cffBlueRound(x)
		if e != nil {
			return CFFBlueStateFixedExperimental{}, e
		}
	}
	return s, nil
}

// CaptureCFFBlueStemFixedExperimental tries zones in FreeType's combined
// bottom/top source order. It makes a deep copy and never mutates the caller.
// Locked stems are not recaptured. A caught pair translates both edges by the
// same amount. No adjustment/initial-map builder is performed here.
func CaptureCFFBlueStemFixedExperimental(s CFFBlueStateFixedExperimental, h CFFSourceHorizontalHintExperimental) (CFFSourceHorizontalHintExperimental, bool, error) {
	clone := h
	if h.Hint.Bottom != nil {
		b := *h.Hint.Bottom
		clone.Hint.Bottom = &b
	}
	if h.Hint.Top != nil {
		t := *h.Hint.Top
		clone.Hint.Top = &t
	}
	if clone.Locked {
		return clone, false, nil
	}
	if clone.Hint.Bottom == nil && clone.Hint.Top == nil {
		return clone, false, fontErr("CFF Blue no hint edges")
	}
	for _, z := range s.Zones {
		var edge *CFFHintEdgeFixedExperimental
		if z.BottomZone {
			edge = clone.Hint.Bottom
		} else {
			edge = clone.Hint.Top
		}
		if edge == nil {
			continue
		}
		if edge.CharacterY < z.BottomCS-s.BlueFuzz || edge.CharacterY > z.TopCS+s.BlueFuzz {
			continue
		}
		dsNew := int64(0)
		if s.SuppressOvershoot {
			dsNew = z.FlatDS
		} else if z.BottomZone && z.TopCS-edge.CharacterY >= s.BlueShift {
			r, er := cffBlueRound(edge.DeviceY)
			if er != nil {
				return clone, false, er
			}
			dsNew = r
			if dsNew > z.FlatDS-65536 {
				dsNew = z.FlatDS - 65536
			}
		} else if !z.BottomZone && edge.CharacterY-z.BottomCS >= s.BlueShift {
			r, er := cffBlueRound(edge.DeviceY)
			if er != nil {
				return clone, false, er
			}
			dsNew = r
			if dsNew < z.FlatDS+65536 {
				dsNew = z.FlatDS + 65536
			}
		} else {
			var er error
			dsNew, er = cffBlueRound(edge.DeviceY)
			if er != nil {
				return clone, false, er
			}
		}
		delta := dsNew - edge.DeviceY
		for _, v := range []*CFFHintEdgeFixedExperimental{clone.Hint.Bottom, clone.Hint.Top} {
			if v != nil {
				v.DeviceY += delta
				if v.DeviceY < math.MinInt32 || v.DeviceY > math.MaxInt32 {
					return clone, false, fontErr("CFF Blue capture overflows")
				}
			}
		}
		clone.Locked = true
		return clone, true, nil
	}
	return clone, false, nil
}
