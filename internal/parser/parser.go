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

// RSUVest represents a restricted stock unit vesting event.
type RSUVest struct {
	AccountID           uuid.UUID
	SecurityISIN        string
	VestDate            time.Time
	GrantNumber         string
	GrossQuantity       float64
	NetQuantity         float64
	Price               float64
	Currency            string
	Vested              bool
	LinkTransactionHash string
	ImportHash          string
}

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
	RSUVests      int      `json:"rsu_vests,omitempty"`
	NewSecurities []string `json:"new_securities"`
	Institution   string   `json:"institution"`
	Errors        []string `json:"errors,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
	AccountType   string   `json:"account_type,omitempty"` // detected from CSV (e.g., "brokerage", "savings")
}

// ParseResult holds parsed transactions and any warnings about skipped rows.
type ParseResult struct {
	Transactions []Transaction
	Warnings     []string
}

// Parser defines the interface for institution-specific CSV parsers.
type Parser interface {
	// Detect returns true if the header matches this institution.
	Detect(header []string) bool
	// Parse transforms raw CSV records into normalized transactions and optional RSU vests.
	Parse(records [][]string, accountID uuid.UUID) ([]Transaction, []RSUVest, error)
	// Institution returns the institution identifier.
	Institution() string
}

// WarningParser is optionally implemented by parsers that collect per-row warnings.
type WarningParser interface {
	Warnings() []string
}

// AccountTypeDetector is optionally implemented by parsers that can detect the
// account type from CSV content (e.g., "brokerage" or "savings" column).
type AccountTypeDetector interface {
	DetectedAccountType() string
}

// registeredParsers are tried in order during auto-detection.
var registeredParsers = []Parser{
	&MorganStanleyParser{},
	&SparkasseParser{},
	&N26Parser{},
	&ScalableCapitalParser{},
	&RevolutParser{},
	&INGParser{},
	&DeltaParser{},
	&WealthExportParser{},
	&HoldingsTemplateParser{},
}

// ParseCSV reads raw bytes, detects the institution, and returns normalized transactions and optional RSU vests.
func ParseCSV(data []byte, accountID uuid.UUID) ([]Transaction, []RSUVest, *ImportResult, error) {
	// Try UTF-8 first, fallback to Latin-1
	text := tryDecode(data)

	// Detect delimiter
	delimiter := detectDelimiter(text)

	reader := csv.NewReader(strings.NewReader(text))
	reader.Comma = delimiter
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1 // allow variable field counts (e.g., title rows, footers)

	records, err := reader.ReadAll()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse CSV: %w", err)
	}

	if len(records) < 2 {
		return nil, nil, nil, fmt.Errorf("CSV file has no data rows")
	}

	// Auto-detect institution. Try the first row as header; if no match,
	// try subsequent rows (some formats — e.g. ING Ordermanager and
	// Depotübersicht — prefix the real header with several title/blank rows).
	headerIdx := 0
	var matchedParser Parser
	for hi := 0; hi < len(records) && hi < 10; hi++ {
		for _, p := range registeredParsers {
			if p.Detect(records[hi]) {
				headerIdx = hi
				matchedParser = p
				break
			}
		}
		if matchedParser != nil {
			break
		}
	}

	if matchedParser != nil {
		p := matchedParser
		parseRecords := records[headerIdx:]
		txns, vests, err := p.Parse(parseRecords, accountID)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("parse %s CSV: %w", p.Institution(), err)
		}

		// Compute import hashes for transactions that don't already have one
		for i := range txns {
			if txns[i].ImportHash == "" {
				txns[i].ImportHash = computeHash(txns[i])
			}
		}

		// Link vest rows to their sibling transaction hashes
		for i := range vests {
			if vests[i].LinkTransactionHash != "" {
				for _, txn := range txns {
					if txn.Reference == vests[i].LinkTransactionHash {
						vests[i].LinkTransactionHash = txn.ImportHash
						break
					}
				}
			}
		}

		// Collect warnings from parser (if supported)
		var warnings []string
		if wp, ok := p.(WarningParser); ok {
			warnings = append(warnings, wp.Warnings()...)
		}

		// Summary warning for total skipped rows
		dataRows := len(parseRecords) - 1 // exclude header
		parsedRows := len(txns)
		if skippedRows := dataRows - parsedRows; skippedRows > 0 && len(warnings) == 0 {
			warnings = append(warnings, fmt.Sprintf("%d rows skipped during parsing (empty, cancelled, or unsettled)", skippedRows))
		}

		result := &ImportResult{
			Institution: p.Institution(),
			Errors:      warnings,
		}

		// Detect account type from CSV if parser supports it
		if atd, ok := p.(AccountTypeDetector); ok {
			result.AccountType = atd.DetectedAccountType()
		}

		return txns, vests, result, nil
	}

	return nil, nil, nil, fmt.Errorf("unrecognized CSV format: could not detect institution from header %v", records[0])
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

// detectDelimiter guesses whether the CSV uses semicolons or commas by
// counting occurrences across the first several lines. A multi-line sample
// is needed because some exports (e.g. ING Depotübersicht) begin with a
// human-readable title line that contains neither delimiter.
func detectDelimiter(text string) rune {
	sample := text
	for i, n := 0, 0; i < len(text); i++ {
		if text[i] == '\n' {
			n++
			if n >= 10 {
				sample = text[:i]
				break
			}
		}
	}
	if strings.Count(sample, ";") > strings.Count(sample, ",") {
		return ';'
	}
	return ','
}

// computeHash generates a SHA-256 deduplication hash from key transaction fields.
// Includes AccountID so the same deposit to two different accounts is allowed.
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

// abs returns the absolute value of a float64.
func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// ContentHash generates a hash excluding AccountID, used to detect when the
// same CSV is accidentally imported into a different account.
func ContentHash(t Transaction) string {
	data := fmt.Sprintf("%s|%s|%.4f|%s|%s",
		t.Type,
		t.Date.Format("2006-01-02"),
		t.Amount,
		t.Reference,
		t.Counterparty,
	)
	hash := sha256.Sum256([]byte(data))
	return fmt.Sprintf("%x", hash)
}
