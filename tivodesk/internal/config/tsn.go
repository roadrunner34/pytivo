package config

// A TiVo Service Number (TSN) starts with a model prefix. pyTivo decides
// what a box can do from that prefix alone, and these functions reproduce
// its rules exactly, including their gaps: a newer box such as the Edge
// (prefix D6E) counts as HD here even though it can play 4K. Recognising
// newer models is tracked as an open decision in docs/TIVODESK_PLAN.md.

// IsHD reports whether the TSN belongs to a high-definition TiVo.
func IsHD(tsn string) bool {
	return tsn != "" && tsn[0] >= '6' && !hasPrefix3(tsn, "649")
}

// Is4K reports whether the TSN belongs to a 4K TiVo (Bolt).
func Is4K(tsn string) bool {
	return hasPrefix3(tsn, "849") || hasPrefix3(tsn, "8F9")
}

// IsTSCapable reports whether the TiVo accepts MPEG transport streams.
func IsTSCapable(tsn string) bool {
	return tsn != "" && (tsn[0] >= '7' || hasPrefix3(tsn, "663"))
}

// Is169Letterbox reports whether the TiVo pads 16:9 video for a 4:3 screen.
func Is169Letterbox(tsn string) bool { return hasPrefix3(tsn, "649") }

// Is169Blacklisted reports whether the TiVo cannot pad 16:9 video itself.
func Is169Blacklisted(tsn string) bool {
	return tsn != "" && !IsHD(tsn) && !Is169Letterbox(tsn)
}

func hasPrefix3(tsn, prefix string) bool {
	return len(tsn) >= 3 && tsn[:3] == prefix
}

// ClassOf returns the settings class for a TSN: 4K, HD or SD. An empty TSN
// is SD.
func ClassOf(tsn string) string {
	switch {
	case Is4K(tsn):
		return Class4K
	case IsHD(tsn):
		return ClassHD
	}
	return ClassSD
}

// TivoHeight is the tallest picture the TiVo displays.
func TivoHeight(tsn string) int {
	switch ClassOf(tsn) {
	case Class4K:
		return 2160
	case ClassHD:
		return 1080
	}
	return 480
}

// TivoWidth is the widest picture the TiVo displays.
func TivoWidth(tsn string) int {
	switch ClassOf(tsn) {
	case Class4K:
		return 3840
	case ClassHD:
		return 1920
	}
	return 544
}

var (
	validWidths  = []int{1920, 1440, 1280, 720, 704, 544, 480, 352}
	validHeights = []int{1080, 720, 480}
)

// nearest returns the value closest to x. On a tie the larger value wins.
func nearest(x int, values []int) int {
	best := values[0]
	for _, v := range values[1:] {
		db, dv := abs(x-best), abs(x-v)
		if dv < db || (dv == db && v > best) {
			best = v
		}
	}
	return best
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// NearestTivoWidth snaps a width to one a TiVo supports.
func NearestTivoWidth(width int) int { return nearest(width, validWidths) }

// NearestTivoHeight snaps a height to one a TiVo supports.
func NearestTivoHeight(height int) int { return nearest(height, validHeights) }
