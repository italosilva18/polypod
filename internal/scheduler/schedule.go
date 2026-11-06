package scheduler

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// scheduleSpec is deliberately small: a routine can run once, at a fixed
// interval, or according to a five-field cron expression.
type scheduleSpec struct {
	kind     string
	interval time.Duration
	at       time.Time
	fields   [5]cronField
}

type cronField struct {
	values   map[int]bool
	wildcard bool
}

var cronBounds = [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 7}}

func parseSchedule(expr string) (scheduleSpec, error) {
	expr = strings.TrimSpace(expr)
	if strings.HasPrefix(expr, "@every ") {
		interval, err := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(expr, "@every ")))
		if err != nil || interval < time.Second {
			return scheduleSpec{}, fmt.Errorf("intervalo invalido: %q", expr)
		}
		return scheduleSpec{kind: "every", interval: interval}, nil
	}
	if strings.HasPrefix(expr, "@at ") {
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(strings.TrimPrefix(expr, "@at ")))
		if err != nil {
			return scheduleSpec{}, fmt.Errorf("data RFC3339 invalida: %q", expr)
		}
		return scheduleSpec{kind: "at", at: at}, nil
	}
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return scheduleSpec{}, fmt.Errorf("cron precisa de cinco campos: %q", expr)
	}
	spec := scheduleSpec{kind: "cron"}
	for i, part := range parts {
		field, err := parseCronField(part, cronBounds[i][0], cronBounds[i][1], i == 4)
		if err != nil {
			return scheduleSpec{}, fmt.Errorf("campo cron %d: %w", i+1, err)
		}
		spec.fields[i] = field
	}
	return spec, nil
}

func parseCronField(expr string, min, max int, weekday bool) (cronField, error) {
	field := cronField{values: make(map[int]bool), wildcard: expr == "*"}
	for _, item := range strings.Split(expr, ",") {
		if item == "" {
			return cronField{}, fmt.Errorf("lista vazia em %q", expr)
		}
		base, stepText, hasStep := strings.Cut(item, "/")
		step := 1
		if hasStep {
			var err error
			step, err = strconv.Atoi(stepText)
			if err != nil || step < 1 || step > max-min+1 {
				return cronField{}, fmt.Errorf("passo invalido em %q", item)
			}
		}
		start, end := min, max
		switch {
		case base == "*":
		case strings.Contains(base, "-"):
			a, b, ok := strings.Cut(base, "-")
			if !ok {
				return cronField{}, fmt.Errorf("intervalo invalido em %q", item)
			}
			var err error
			start, err = strconv.Atoi(a)
			if err != nil {
				return cronField{}, fmt.Errorf("inicio invalido em %q", item)
			}
			end, err = strconv.Atoi(b)
			if err != nil {
				return cronField{}, fmt.Errorf("fim invalido em %q", item)
			}
		default:
			var err error
			start, err = strconv.Atoi(base)
			if err != nil {
				return cronField{}, fmt.Errorf("numero invalido em %q", item)
			}
			end = start
			if hasStep {
				end = max
			}
		}
		if start < min || end > max || start > end {
			return cronField{}, fmt.Errorf("valor fora do intervalo %d-%d em %q", min, max, item)
		}
		for n := start; n <= end; n += step {
			if weekday && n == 7 {
				field.values[0] = true
			} else {
				field.values[n] = true
			}
		}
	}
	if len(field.values) == 0 {
		return cronField{}, fmt.Errorf("campo vazio em %q", expr)
	}
	return field, nil
}

func (s scheduleSpec) matches(at time.Time) bool {
	if s.kind != "cron" {
		return false
	}
	values := [5]int{at.Minute(), at.Hour(), at.Day(), int(at.Month()), int(at.Weekday())}
	if !s.fields[0].values[values[0]] || !s.fields[1].values[values[1]] || !s.fields[3].values[values[3]] {
		return false
	}
	day := s.fields[2].values[values[2]]
	weekday := s.fields[4].values[values[4]]
	// Preserve the existing conjunction for day-of-month and weekday so jobs
	// saved by earlier Polypod versions do not silently change their cadence.
	return day && weekday
}

func nextOccurrence(expr string, after time.Time, zone string) (time.Time, error) {
	spec, err := parseSchedule(expr)
	if err != nil {
		return time.Time{}, err
	}
	if spec.kind == "at" {
		if spec.at.After(after) {
			return spec.at, nil
		}
		return time.Time{}, nil
	}
	if spec.kind == "every" {
		return after.Add(spec.interval), nil
	}
	loc := time.Local
	if zone != "" {
		loc, err = time.LoadLocation(zone)
		if err != nil {
			return time.Time{}, fmt.Errorf("fuso horario %q: %w", zone, err)
		}
	}
	candidate := after.In(loc).Truncate(time.Minute).Add(time.Minute)
	// Eight years includes the next leap-day occurrence. Invalid combinations
	// (such as February 30) are rejected instead of creating dormant jobs.
	for i := 0; i < 8*366*24*60; i++ {
		if spec.matches(candidate) {
			return candidate, nil
		}
		candidate = candidate.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("cron %q nao possui proxima ocorrencia", expr)
}
