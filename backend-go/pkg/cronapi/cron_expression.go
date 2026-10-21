package cronapi

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

var macros = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

var monthNames = []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}
var dowNames = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

type fieldSpec struct {
	name      string
	min       int
	max       int
	names     []string
	normalize func(int) int
}

var specs = []fieldSpec{
	{name: "minute", min: 0, max: 59},
	{name: "hour", min: 0, max: 23},
	{name: "day-of-month", min: 1, max: 31},
	{name: "month", min: 1, max: 12, names: monthNames},
	{name: "day-of-week", min: 0, max: 7, names: dowNames, normalize: func(v int) int {
		if v == 7 {
			return 0
		}
		return v
	}},
}

// ParsedCronExpression models a validated 5-field cron expression.
type ParsedCronExpression struct {
	Expression    string
	Minutes       []int
	Hours         []int
	DaysOfMonth   []int
	Months        []int
	DaysOfWeek    []int
	DOMRestricted bool
	DOWRestricted bool
}

func resolveValue(token string, spec fieldSpec) (int, error) {
	lower := strings.ToLower(token)
	if len(spec.names) > 0 {
		for idx, name := range spec.names {
			if name == lower {
				if spec.name == "day-of-week" {
					return idx, nil
				}
				return idx + 1, nil
			}
		}
	}
	val, err := strconv.Atoi(token)
	if err != nil {
		return 0, fmt.Errorf("Invalid %s value %q", spec.name, token)
	}
	if val < spec.min || val > spec.max {
		return 0, fmt.Errorf("%s value %d out of range %d-%d", spec.name, val, spec.min, spec.max)
	}
	return val, nil
}

func parseField(raw string, spec fieldSpec) ([]int, bool, error) {
	field := strings.TrimSpace(raw)
	if field == "" {
		return nil, false, fmt.Errorf("Empty %s field", spec.name)
	}

	restricted := field != "*" && field != "?"
	valueSet := make(map[int]bool)

	for _, part := range strings.Split(field, ",") {
		segment := strings.TrimSpace(part)
		if segment == "" {
			return nil, false, fmt.Errorf("Empty %s list entry in %q", spec.name, raw)
		}

		slashParts := strings.Split(segment, "/")
		if len(slashParts) > 2 {
			return nil, false, fmt.Errorf("Invalid %s segment %q", spec.name, segment)
		}

		base := slashParts[0]
		step := 1
		if len(slashParts) == 2 {
			st, err := strconv.Atoi(slashParts[1])
			if err != nil || st <= 0 {
				return nil, false, fmt.Errorf("Invalid step %q in %s field", slashParts[1], spec.name)
			}
			step = st
		}

		var start, end int
		if base == "*" || base == "?" {
			start = spec.min
			end = spec.max
		} else if strings.Contains(base, "-") {
			dashParts := strings.Split(base, "-")
			if len(dashParts) != 2 {
				return nil, false, fmt.Errorf("Invalid %s range %q", spec.name, base)
			}
			a, err := resolveValue(dashParts[0], spec)
			if err != nil {
				return nil, false, err
			}
			b, err := resolveValue(dashParts[1], spec)
			if err != nil {
				return nil, false, err
			}
			if a > b {
				return nil, false, fmt.Errorf("Invalid %s range %q (start after end)", spec.name, base)
			}
			start = a
			end = b
		} else {
			v, err := resolveValue(base, spec)
			if err != nil {
				return nil, false, err
			}
			start = v
			if len(slashParts) == 2 {
				end = spec.max
			} else {
				end = start
			}
		}

		for v := start; v <= end; v += step {
			norm := v
			if spec.normalize != nil {
				norm = spec.normalize(v)
			}
			valueSet[norm] = true
		}
	}

	out := make([]int, 0, len(valueSet))
	for v := range valueSet {
		out = append(out, v)
	}
	sort.Ints(out)
	return out, restricted, nil
}

// ParseCronExpression parses and validates a 5-field cron expression.
func ParseCronExpression(expression string) (ParsedCronExpression, error) {
	trimmed := strings.TrimSpace(expression)
	if trimmed == "" {
		return ParsedCronExpression{}, errors.New("Cron expression is required")
	}

	expanded := trimmed
	if m, ok := macros[strings.ToLower(trimmed)]; ok {
		expanded = m
	}

	fields := strings.Fields(expanded)
	if len(fields) != 5 {
		return ParsedCronExpression{}, fmt.Errorf("Cron expression must have exactly 5 fields, got %d", len(fields))
	}

	mins, _, err := parseField(fields[0], specs[0])
	if err != nil {
		return ParsedCronExpression{}, err
	}
	hours, _, err := parseField(fields[1], specs[1])
	if err != nil {
		return ParsedCronExpression{}, err
	}
	dom, domRestr, err := parseField(fields[2], specs[2])
	if err != nil {
		return ParsedCronExpression{}, err
	}
	months, _, err := parseField(fields[3], specs[3])
	if err != nil {
		return ParsedCronExpression{}, err
	}
	dow, dowRestr, err := parseField(fields[4], specs[4])
	if err != nil {
		return ParsedCronExpression{}, err
	}

	return ParsedCronExpression{
		Expression:    strings.Join(fields, " "),
		Minutes:       mins,
		Hours:         hours,
		DaysOfMonth:   dom,
		Months:        months,
		DaysOfWeek:    dow,
		DOMRestricted: domRestr,
		DOWRestricted: dowRestr,
	}, nil
}

// ValidateCronExpression validates an expression, returning canonical form if valid.
func ValidateCronExpression(expression string) (bool, string, string) {
	parsed, err := ParseCronExpression(expression)
	if err != nil {
		return false, err.Error(), ""
	}
	return true, "", parsed.Expression
}

const maxSearchMinutes = 5 * 366 * 24 * 60

func containsInt(arr []int, target int) bool {
	for _, v := range arr {
		if v == target {
			return true
		}
	}
	return false
}

// NextCronRun calculates the next run timestamp strictly after fromMs.
// Returns nil if it will never fire.
func NextCronRun(parsed ParsedCronExpression, fromMs int64) *int64 {
	from := time.UnixMilli(fromMs).Local()
	// Next whole minute strictly after from
	start := from.Truncate(time.Minute).Add(time.Minute)

	cursor := start
	seen := make(map[string]bool)

	for i := 0; i < maxSearchMinutes; i++ {
		monthVal := int(cursor.Month())
		if !containsInt(parsed.Months, monthVal) {
			// Skip to next month
			year, month, _ := cursor.Date()
			cursor = time.Date(year, month+1, 1, 0, 0, 0, 0, cursor.Location())
			continue
		}

		key := fmt.Sprintf("%d-%d-%d", cursor.Year(), cursor.Month(), cursor.Day())
		if !seen[key] {
			seen[key] = true
			domMatch := containsInt(parsed.DaysOfMonth, cursor.Day())
			dowMatch := containsInt(parsed.DaysOfWeek, int(cursor.Weekday()))

			var dayMatches bool
			if parsed.DOMRestricted && parsed.DOWRestricted {
				dayMatches = domMatch || dowMatch
			} else {
				dayMatches = domMatch && dowMatch
			}

			if !dayMatches {
				cursor = time.Date(cursor.Year(), cursor.Month(), cursor.Day()+1, 0, 0, 0, 0, cursor.Location())
				continue
			}
		}

		if !containsInt(parsed.Hours, cursor.Hour()) {
			cursor = time.Date(cursor.Year(), cursor.Month(), cursor.Day(), cursor.Hour()+1, 0, 0, 0, cursor.Location())
			continue
		}

		if !containsInt(parsed.Minutes, cursor.Minute()) {
			cursor = cursor.Add(time.Minute)
			continue
		}

		next := cursor.UnixMilli()
		return &next
	}

	return nil
}
