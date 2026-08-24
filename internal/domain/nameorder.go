package domain

import "unicode"

// NameLess orders repo names the way a file browser lists files: case-insensitive, and runs of
// digits compared as numbers rather than characters, so "repo2" sorts before "repo10" and
// "amethyst-stack" sorts before "FantasyBaccaratSynthesizer".
//
// init, AddRepo and fmt all order the repos: sequence by name (spec §5.2); they must agree on
// exactly this comparator or fmt would perpetually reorder what add just wrote.
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
// parsing them into a fixed-width integer -- a repo name is not obliged to keep its numbers small.
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
