package reconcilesvc

import (
	"fmt"
	"strings"
	"time"
)

// parseISODuration parses an ISO 8601 duration such as P7D, PT2H or P1W2DT3H
// (years as 365 days, months as 30).
func parseISODuration(s string) (time.Duration, error) {
	if !strings.HasPrefix(s, "P") || len(s) < 3 {
		return 0, fmt.Errorf("not an ISO 8601 duration")
	}
	var total time.Duration
	inTime, num := false, ""
	units := map[bool]map[byte]time.Duration{
		false: {'Y': 365 * 24 * time.Hour, 'M': 30 * 24 * time.Hour, 'W': 7 * 24 * time.Hour, 'D': 24 * time.Hour},
		true:  {'H': time.Hour, 'M': time.Minute, 'S': time.Second},
	}
	for i := 1; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == 'T':
			inTime = true
		case (ch >= '0' && ch <= '9') || ch == '.':
			num += string(ch)
		default:
			unit, ok := units[inTime][ch]
			if !ok || num == "" {
				return 0, fmt.Errorf("bad component %q", string(ch))
			}
			var v float64
			if _, err := fmt.Sscanf(num, "%g", &v); err != nil {
				return 0, err
			}
			total += time.Duration(v * float64(unit))
			num = ""
		}
	}
	if num != "" {
		return 0, fmt.Errorf("trailing number without unit")
	}
	return total, nil
}
