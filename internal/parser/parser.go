package parser

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

// Transaction represents a normalized transaction from any institution.
type Transaction struct {
	AccountID    uuid.UUID
	Date         time.Time
	Type         string // buy, sell, dividend, interest, deposit, withdrawal, fee, transfer, savings_plan
	SecurityISIN string // empty for cash transactions
	Quantity     float64
	Price        float64
	Amount       float64
	Fee          float64
	Tax          float64
	Currency     string
	Counterparty string
	Reference    string
	Category     string
	ImportHash   string
}

// ImportResult contains summary statistics from a CSV import.
type ImportResult struct {
	Imported      int      `json:"imported"`
	Skipped       int      `json:"skipped"`
	NewSecurities []string `json:"new_securities"`
	Institution   string   `json:"institution"`
	Errors        []string `json:"errors,omitempty"`
}

// Parser defines the interface for institution-specific CSV parsers.
type Parser interface {
	// Detect returns true if the header matches this institution.
	Detect(header []string) bool
	// Parse transforms raw CSV records into normalized transactions.
	Parse(records [][]string, accountID uuid.UUID) ([]Transaction, error)
	// Institution returns the institution identifier.
	Institution() string
}

// registeredParsers are tried in order during auto-detection.
var registeredParsers = []Parser{
	&SparkasseParser{},
	&N26Parser{},
	&ScalableCapitalParser{},
	&HoldingsTemplateParser{},
}

// ParseCSV reads raw bytes, detects the institution, and returns normalized transactions.
func ParseCSV(data []byte, accountID uuid.UUID) ([]Transaction, *ImportResult, error) {
	// Try UTF-8 first, fallback to Latin-1
	text := tryDecode(data)

	// Detect delimiter
	delimiter := detectDelimiter(text)

	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma = delimiter
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("parse CSV: %w", err)
	}

	if len(records) < 2 {
		return nil, nil, fmt.Errorf("CSV file has no data rows")
	}

	header := records[0]

	// Auto-detect institution
	for _, p := range registeredParsers {
		if p.Detect(header) {
			txns, err := p.Parse(records, accountID)
			if err != nil {
				return nil, nil, fmt.Errorf("parse %s CSV: %w", p.Institution(), err)
			}

			// Compute import hashes
			for i := range txns {
				txns[i].ImportHash = computeHash(txns[i])
			}

			result := &ImportResult{
				Institution: p.Institution(),
			}
			return txns, result, nil
		}
	}

	return nil, nil, fmt.Errorf("unrecognized CSV format: could not detect institution from header %v", header)
}

// tryDecode attempts UTF-8, falls back to Latin-1 if invalid UTF-8 sequences are found.
func tryDecode(data []byte) string {
	// Check for UTF-8 BOM
	if bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
		data = data[3:]
	}

	// Quick check: if it's valid UTF-8, use as-is
	text := string(data)
	for _, r := range text {
		if r == '\uFFFD' {
			// Invalid UTF-8 detected, try Latin-1
			reader := transform.NewReader(bytes.NewReader(data), charmap.ISO8859_1.NewDecoder())
			decoded, err := io.ReadAll(reader)
			if err == nil {
				return string(decoded)
			}
			break
		}
	}
	return text
}

// detectDelimiter guesses whether the CSV uses semicolons or commas.
func detectDelimiter(text string) rune {
	// Look at the first line
	firstLine := text
	if idx := strings.IndexByte(text, '\n'); idx > 0 {
		firstLine = text[:idx]
	}

	semicolons := strings.Count(firstLine, ";")
	commas := strings.Count(firstLine, ",")

	if semicolons > commas {
		return ';'
	}
	return ','
}

// computeHash generates a SHA-256 deduplication hash from key transaction fields.
func computeHash(t Transaction) string {
	data := fmt.Sprintf("%s|%s|%.4f|%s|%s",
		t.AccountID.String(),
		t.Date.Format("2006-01-02"),
		t.Amount,
		t.Reference,
		t.Counterparty,
	)
	hash := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", hash)
}
