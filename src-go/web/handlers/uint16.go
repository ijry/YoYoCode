package handlers

import "strconv"

func uint16Val(v any, def uint16) uint16 {
	if f, ok := v.(float64); ok && f > 0 {
		return uint16(f)
	}
	s, ok := v.(string)
	if !ok {
		return def
	}
	if n, err := strconv.ParseUint(s, 10, 16); err == nil && n > 0 {
		return uint16(n)
	}
	return def
}
