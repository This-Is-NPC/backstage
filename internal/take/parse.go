package take

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ParseDuration accepts Go durations and a day count such as 7d.
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	if d < 0 {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return d, nil
}

// ParseSize accepts a byte count with an optional K, M, or G suffix (1024).
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	mult := int64(1)
	switch {
	case hasByteSuffix(s, "K"):
		mult, s = 1024, s[:len(s)-1]
	case hasByteSuffix(s, "M"):
		mult, s = 1024*1024, s[:len(s)-1]
	case hasByteSuffix(s, "G"):
		mult, s = 1024*1024*1024, s[:len(s)-1]
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return int64(n * float64(mult)), nil
}

func hasByteSuffix(s, suf string) bool {
	if len(s) < 2 {
		return false
	}
	last := s[len(s)-1]
	if unicode.ToUpper(rune(last)) != rune(suf[0]) {
		return false
	}
	return unicode.IsDigit(rune(s[len(s)-2])) || s[len(s)-2] == '.'
}
