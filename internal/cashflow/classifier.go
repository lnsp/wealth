// Package cashflow classifies cash transactions into spending categories.
//
// The classifier is intentionally simple: deterministic keyword matching over
// counterparty + reference text, with the existing transactions.category
// column acting as a user override (taking precedence over the heuristic).
//
// Categories are German-tailored — payroll keywords ("GEHALT", "LOHN"),
// landlord patterns ("MIETE", "KALTMIETE"), supermarkets (REWE/Edeka/
// Lidl/Aldi), and so on. Anything that doesn't match lands in `other`.
package cashflow

import (
	"strings"
	"unicode"
)

// Bucket is the top-level cashflow class used to compute Net Surplus.
type Bucket string

const (
	BucketIncome   Bucket = "income"   // payroll, interest, dividends paid out, refunds
	BucketFixed    Bucket = "fixed"    // rent, utilities, insurance, subscriptions
	BucketVariable Bucket = "variable" // groceries, dining, transport, shopping, etc.
	BucketTransfer Bucket = "transfer" // internal moves (savings, brokerage funding) — excluded from spend
)

// Category is the finer-grained label shown in the breakdown chart.
type Category string

const (
	CatSalary        Category = "salary"
	CatInterest      Category = "interest"
	CatRefund        Category = "refund"
	CatHousing       Category = "housing"
	CatUtilities     Category = "utilities"
	CatInsurance     Category = "insurance"
	CatSubscriptions Category = "subscriptions"
	CatGroceries     Category = "groceries"
	CatDining        Category = "dining"
	CatTransport     Category = "transport"
	CatHealth        Category = "health"
	CatEntertainment Category = "entertainment"
	CatShopping      Category = "shopping"
	CatTax           Category = "tax"
	CatInvestment    Category = "investment"
	CatInternal      Category = "internal"
	// CatAssetPurchase marks the payment for a tracked physical asset (car):
	// cash turned into an asset, so it is a transfer rather than spending.
	CatAssetPurchase Category = "asset_purchase"
	// CatLoanPayment marks a matched loan instalment. The cashflow view splits
	// it into interest (housing/transport) and CatLoanPrincipal (saving).
	CatLoanPayment   Category = "loan_payment"
	CatLoanPrincipal Category = "loan_principal"
	CatOther         Category = "other"
)

// BucketOf maps a Category to its top-level Bucket.
func BucketOf(c Category) Bucket {
	switch c {
	case CatSalary, CatInterest, CatRefund:
		return BucketIncome
	case CatHousing, CatUtilities, CatInsurance, CatSubscriptions:
		return BucketFixed
	case CatInvestment, CatInternal, CatAssetPurchase, CatLoanPayment, CatLoanPrincipal:
		return BucketTransfer
	default:
		return BucketVariable
	}
}

// wordBoundedKeywords lists short or ambiguous uppercase keywords that must match
// on non-alphanumeric word boundaries (preventing e.g. "TK" inside "MARKTKAUF"
// or "KREDITKARTE", "GAS" inside "GASTHAUS", "TAX" inside "TAXI", "TIER" inside
// "TIERARZT", and "UBER" inside "HUBER").
var wordBoundedKeywords = map[string]bool{
	"TK": true, "AOK": true, "DAK": true, "HUK": true, "AXA": true, "DEVK": true, "ERGO": true,
	"GAS": true, "EON": true, "RWE": true, "O2": true, "RENT": true, "TAX": true,
	"TIER": true, "UBER": true, "JET": true, "TOTAL": true, "ESSO": true, "ARAL": true,
	"BVG": true, "MVV": true, "VRR": true, "RMV": true, "HVV": true, "ADAC": true,
	"BOLT": true, "LIME": true, "ABO": true, "OTTO": true, "OBI": true, "TOOM": true,
	"C&A": true, "H&M": true, "ZARA": true, "FITX": true, "STEAM": true, "XBOX": true,
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func hasWordBoundaryMatch(hay, kw string) bool {
	start := 0
	for {
		idx := strings.Index(hay[start:], kw)
		if idx < 0 {
			return false
		}
		pos := start + idx
		end := pos + len(kw)
		leftOK := pos == 0
		if !leftOK {
			// Decode preceding rune
			for i := pos - 1; i >= 0; i-- {
				if (hay[i] & 0xC0) != 0x80 {
					r := []rune(hay[i:pos])[0]
					leftOK = !isWordRune(r)
					break
				}
			}
		}
		rightOK := end == len(hay)
		if !rightOK {
			r := []rune(hay[end:])[0]
			rightOK = !isWordRune(r)
		}
		if leftOK && rightOK {
			return true
		}
		start = pos + 1
	}
}

func matchesKeyword(hay, rawKw string) bool {
	kw := strings.TrimSpace(rawKw)
	if kw == "" {
		return false
	}
	if wordBoundedKeywords[kw] {
		return hasWordBoundaryMatch(hay, kw)
	}
	return strings.Contains(hay, kw)
}

// keyword rules; order matters within each list — first match wins.
// Keys are uppercase, matched against UPPER(counterparty + " " + reference).
var rules = []struct {
	keywords []string
	category Category
}{
	{[]string{"GEHALT", "LOHN", "SALARY", "PAYROLL", "BEZÜGE", "BEZUEGE"}, CatSalary},
	{[]string{"ZINSEN", "INTEREST"}, CatInterest},
	{[]string{"ERSTATTUNG", "REFUND", "RÜCKZAHLUNG", "RUECKZAHLUNG"}, CatRefund},

	{[]string{"MIETE", "KALTMIETE", "WARMMIETE", "NEBENKOSTEN", "HAUSGELD", "RENT"}, CatHousing},
	{[]string{"STROM", "ERDGAS", "GASAG", "GASWERK", "GASVERSORG", "GAS", "WASSER", "ABWASSER", "MUELL", "MÜLL", "STADTWERKE", "VATTENFALL", "EON", "ENBW", "RWE", "TELEKOM", "VODAFONE", "1&1", "O2", "PYUR", "INTERNET"}, CatUtilities},
	{[]string{"VERSICHERUNG", "ALLIANZ", "HUK", "AXA", "ERGO", "DEVK", "DEBEKA", "GENERALI", "BARMER", "TECHNIKER KRANKENKASSE", "TK", "AOK", "DAK", "KRANKENKASSE", "KRANKENVERSICHERUNG", "INSURANCE", "HAFTPFLICHT"}, CatInsurance},
	{[]string{"NETFLIX", "SPOTIFY", "AMAZON PRIME", "DISNEY", "APPLE.COM", "GITHUB", "ADOBE", "MICROSOFT", "ICLOUD", "DROPBOX", "YOUTUBE", "PATREON", "SUBSCRIPTION", "MONATSABO", "JAHRESABO", "ABO"}, CatSubscriptions},

	{[]string{"REWE", "EDEKA", "LIDL", "ALDI", "PENNY", "NETTO", "KAUFLAND", "DM-DROGERIE", "ROSSMANN", "BIO COMPANY", "BUDNI", "MARKTKAUF", "TEGUT", "BACKEREI", "BÄCKEREI", "BAECKEREI"}, CatGroceries},
	{[]string{"RESTAURANT", "GASTHAUS", "GASTSTÄTTE", "GASTSTAETTE", "MCDONALD", "BURGER KING", "STARBUCKS", "PIZZA", "SUSHI", "DOENER", "DÖNER", "DINER", "LIEFERANDO", "WOLT", "UBER EATS", "TOO GOOD TO GO"}, CatDining},
	{[]string{"APOTHEKE", "PHARMACY", "TIERARZT", "ZAHNARZT", "HAUSARZT", "FACHARZT", "ARZTPRAXIS", "DOCTOR", "PHYSIO", "OPTIKER", "FIELMANN"}, CatHealth},
	{[]string{"DB-VERTRIEB", "DEUTSCHE BAHN", "BVG", "MVV", "VRR", "RMV", "HVV", "9-EURO", "DEUTSCHLANDTICKET", "FLIXBUS", "FLIXTRAIN", "TAXI", "TANKE", "TANKSTELLE", "ESSO", "ARAL", "SHELL", "TOTAL", "JET", "BP-TANK", "ADAC", "UBER", "BOLT", "LIME", "TIER"}, CatTransport},
	{[]string{"KINO", "CINEMA", "THEATER", "KONZERT", "TICKETMASTER", "EVENTIM", "STEAM", "PLAYSTATION", "XBOX", "FITNESS", "FITX", "MCFIT", "URBAN SPORTS", "CLEVER FIT"}, CatEntertainment},
	{[]string{"AMAZON", "OTTO", "ZALANDO", "MEDIAMARKT", "SATURN", "IKEA", "H&M", "ZARA", "C&A", "GALERIA", "BAUHAUS", "OBI", "TOOM", "HORNBACH"}, CatShopping},

	{[]string{"FINANZAMT", "STEUER", "TAX"}, CatTax},
}

// Classify returns the inferred Category for a transaction. `txType` is the
// existing transactions.type (deposit, withdrawal, etc.) — used to short-
// circuit obvious cases. `counterparty` and `reference` are matched against
// the keyword rules. Empty strings are tolerated.
func Classify(txType, counterparty, reference string) Category {
	switch txType {
	case "interest":
		return CatInterest
	case "dividend":
		return CatInterest
	case "tax":
		return CatTax
	case "transfer", "transfer_out", "cash_transfer_in", "cash_transfer_out":
		return CatInternal
	case "buy", "sell", "savings_plan", "fee":
		return CatInvestment
	}

	hay := strings.ToUpper(counterparty + " " + reference)
	for _, r := range rules {
		// Outflows (withdrawals) should not be classified as salary or interest income.
		if txType == "withdrawal" && (r.category == CatSalary || r.category == CatInterest) {
			continue
		}
		for _, kw := range r.keywords {
			if matchesKeyword(hay, kw) {
				return r.category
			}
		}
	}
	if hasWordBoundaryMatch(hay, "ARZT") {
		return CatHealth
	}
	return CatOther
}

// ResolveCategory returns the user-override category if present, else the
// heuristic classification. The override must match a known Category; unknown
// override values fall through to the heuristic.
func ResolveCategory(override, txType, counterparty, reference string) Category {
	if override != "" {
		switch strings.ToLower(strings.TrimSpace(override)) {
		case "income":
			return CatSalary
		case "rent":
			return CatHousing
		}
		if known := Category(override); IsKnown(known) {
			return known
		}
	}
	return Classify(txType, counterparty, reference)
}

// IsKnown reports whether c is one of the predefined categories.
func IsKnown(c Category) bool {
	switch c {
	case CatSalary, CatInterest, CatRefund,
		CatHousing, CatUtilities, CatInsurance, CatSubscriptions,
		CatGroceries, CatDining, CatTransport, CatHealth,
		CatEntertainment, CatShopping, CatTax, CatInvestment,
		CatInternal, CatAssetPurchase, CatLoanPayment, CatLoanPrincipal, CatOther:
		return true
	}
	return false
}

// AllCategories returns every known category in display order.
func AllCategories() []Category {
	return []Category{
		CatSalary, CatInterest, CatRefund,
		CatHousing, CatUtilities, CatInsurance, CatSubscriptions,
		CatGroceries, CatDining, CatTransport, CatHealth,
		CatEntertainment, CatShopping, CatTax,
		CatInvestment, CatInternal, CatAssetPurchase, CatLoanPayment, CatLoanPrincipal, CatOther,
	}
}
