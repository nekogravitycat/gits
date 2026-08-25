package domain

import "unicode"

// NameLess orders repo names like a file browser: case-insensitive, digit runs compared numerically
// so "repo2" sorts before "repo10".
//
// CRITICAL: init, AddRepo and fmt all order by name (spec §5.2) and must share exactly this
// comparator, or fmt would perpetually reorder what add just wrote.
func NameLess(a, b string) bool {
	return compareNames(a, b) < 0
}

func compareNames(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	i, j := 0, 0
	for i < len(ra) && j < len(rb) {
		if isDigit(ra[i]) && isDigit(rb[j]) {
			si, sj := i, j
			for i < len(ra) && isDigit(ra[i]) {
				i++
			}
			for j < len(rb) && isDigit(rb[j]) {
				j++
			}
			if c := compareDigitRuns(ra[si:i], rb[sj:j]); c != 0 {
				return c
			}
			continue
		}
		la, lb := unicode.ToLower(ra[i]), unicode.ToLower(rb[j])
		if la != lb {
			if la < lb {
				return -1
			}
			return 1
		}
		i++
		j++
	}
	switch {
	case len(ra)-i < len(rb)-j:
		return -1
	case len(ra)-i > len(rb)-j:
		return 1
	default:
		return 0
	}
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

// compareDigitRuns compares two runs of digits by numeric value, ignoring leading zeros, without
// parsing into a fixed-width integer (a repo name's numbers need not be small).
func compareDigitRuns(a, b []rune) int {
	a = trimLeadingZeros(a)
	b = trimLeadingZeros(b)
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func trimLeadingZeros(r []rune) []rune {
	i := 0
	for i < len(r)-1 && r[i] == '0' {
		i++
	}
	return r[i:]
}
