package parser

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// parseGermanDecimal converts German-format numbers (1.234,56) to float64.
func parseGermanDecimal(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	s = strings.ReplaceAll(s, ".", "")  // remove thousands separator
	s = strings.Replace(s, ",", ".", 1) // swap decimal separator
	return strconv.ParseFloat(s, 64)
}

// parseStandardDecimal converts standard-format numbers (1,234.56 or 1234.56) to float64.
func parseStandardDecimal(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	s = strings.ReplaceAll(s, ",", "") // remove thousands separator
	return strconv.ParseFloat(s, 64)
}

// parseGermanDate parses DD.MM.YYYY format.
func parseGermanDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	t, err := time.Parse("02.01.2006", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse German date %q: %w", s, err)
	}
	return t, nil
}

// parseISODate parses YYYY-MM-DD format.
func parseISODate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse ISO date %q: %w", s, err)
	}
	return t, nil
}

// normalizeHeader trims and lowercases header fields for matching.
func normalizeHeader(header []string) []string {
	out := make([]string, len(header))
	for i, h := range header {
		out[i] = strings.ToLower(strings.TrimSpace(h))
	}
	return out
}

// headerIndex returns a map from normalized header name to column index.
func headerIndex(header []string) map[string]int {
	idx := make(map[string]int, len(header))
	norm := normalizeHeader(header)
	for i, h := range norm {
		idx[h] = i
	}
	return idx
}

// getField safely retrieves a field from a record by index, returning empty string if out of bounds.
func getField(record []string, idx int) string {
	if idx < 0 || idx >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[idx])
}

// defaultDate returns today's date.
func defaultDate() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}
