package parser

import (
	"testing"
	"time"
)

func TestParseStandardDecimal(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    float64
		wantErr bool
	}{
		{name: "simple integer", input: "1234", want: 1234},
		{name: "with decimal", input: "1234.56", want: 1234.56},
		{name: "with thousands separator", input: "1,234.56", want: 1234.56},
		{name: "negative", input: "-45.67", want: -45.67},
		{name: "negative with thousands", input: "-1,234.56", want: -1234.56},
		{name: "zero", input: "0", want: 0},
		{name: "empty string", input: "", want: 0},
		{name: "whitespace only", input: "   ", want: 0},
		{name: "leading/trailing whitespace", input: "  1234.56  ", want: 1234.56},
		{name: "small decimal", input: "0.01", want: 0.01},
		{name: "large number", input: "1,000,000.99", want: 1000000.99},
		{name: "invalid input", input: "abc", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseStandardDecimal(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseStandardDecimal(%q) expected error, got %f", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseStandardDecimal(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("parseStandardDecimal(%q) = %f, want %f", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseGermanDate(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Time
		wantErr bool
	}{
		{
			name:  "standard German date",
			input: "01.03.2026",
			want:  time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:  "end of year",
			input: "31.12.2025",
			want:  time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC),
		},
		{
			name:  "with leading/trailing whitespace",
			input: "  15.06.2026  ",
			want:  time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC),
		},
		{
			name:    "ISO format rejected",
			input:   "2026-03-01",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "invalid format",
			input:   "March 1, 2026",
			wantErr: true,
		},
		{
			name:  "day/month swapped ambiguity still parses as DD.MM",
			input: "15.01.2026",
			want:  time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGermanDate(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseGermanDate(%q) expected error, got %v", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseGermanDate(%q) unexpected error: %v", tt.input, err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("parseGermanDate(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseISODate(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Time
		wantErr bool
	}{
		{
			name:  "standard ISO date",
			input: "2026-03-01",
			want:  time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:  "end of year",
			input: "2025-12-31",
			want:  time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC),
		},
		{
			name:  "with whitespace",
			input: "  2026-06-15  ",
			want:  time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC),
		},
		{
			name:    "German format rejected",
			input:   "01.03.2026",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "invalid format",
			input:   "2026/03/01",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseISODate(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseISODate(%q) expected error, got %v", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseISODate(%q) unexpected error: %v", tt.input, err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("parseISODate(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeHeader(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "mixed case with whitespace",
			input: []string{"  Buchungstag ", "BETRAG", " Währung "},
			want:  []string{"buchungstag", "betrag", "währung"},
		},
		{
			name:  "already lowercase",
			input: []string{"isin", "name", "quantity"},
			want:  []string{"isin", "name", "quantity"},
		},
		{
			name:  "empty slice",
			input: []string{},
			want:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeHeader(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("normalizeHeader() len = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("normalizeHeader()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestHeaderIndex(t *testing.T) {
	header := []string{"Date", "  Amount ", "CURRENCY"}
	idx := headerIndex(header)

	tests := []struct {
		key     string
		wantIdx int
		wantOk  bool
	}{
		{"date", 0, true},
		{"amount", 1, true},
		{"currency", 2, true},
		{"missing", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got, ok := idx[tt.key]
			if ok != tt.wantOk {
				t.Errorf("headerIndex[%q] ok = %v, want %v", tt.key, ok, tt.wantOk)
			}
			if ok && got != tt.wantIdx {
				t.Errorf("headerIndex[%q] = %d, want %d", tt.key, got, tt.wantIdx)
			}
		})
	}
}

func TestGetField(t *testing.T) {
	record := []string{"  hello ", "world", "  foo  "}

	tests := []struct {
		name string
		idx  int
		want string
	}{
		{name: "valid index with whitespace", idx: 0, want: "hello"},
		{name: "valid index clean", idx: 1, want: "world"},
		{name: "negative index", idx: -1, want: ""},
		{name: "out of bounds", idx: 10, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getField(record, tt.idx)
			if got != tt.want {
				t.Errorf("getField(record, %d) = %q, want %q", tt.idx, got, tt.want)
			}
		})
	}
}

func TestFindColumn(t *testing.T) {
	idx := map[string]int{
		"verwendungszweck": 3,
		"betrag":           5,
	}

	tests := []struct {
		name  string
		names []string
		want  int
	}{
		{name: "first match", names: []string{"verwendungszweck"}, want: 3},
		{name: "second match", names: []string{"missing", "betrag"}, want: 5},
		{name: "no match", names: []string{"foo", "bar"}, want: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findColumn(idx, tt.names...)
			if got != tt.want {
				t.Errorf("findColumn() = %d, want %d", got, tt.want)
			}
		})
	}
}
