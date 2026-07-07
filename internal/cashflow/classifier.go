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
	CatOther         Category = "other"
)

// BucketOf maps a Category to its top-level Bucket.
func BucketOf(c Category) Bucket {
	switch c {
	case CatSalary, CatInterest, CatRefund:
		return BucketIncome
	case CatHousing, CatUtilities, CatInsurance, CatSubscriptions:
		return BucketFixed
	case CatInvestment, CatInternal:
		return BucketTransfer
	default:
		return BucketVariable
	}
}

// keyword rules; order matters within each list — first match wins.
// Keys are uppercase, matched as substrings against UPPER(counterparty + " " + reference).
var rules = []struct {
	keywords []string
	category Category
}{
	{[]string{"GEHALT", "LOHN", "SALARY", "PAYROLL", "BEZÜGE", "BEZUEGE"}, CatSalary},
	{[]string{"ZINSEN", "INTEREST"}, CatInterest},
	{[]string{"ERSTATTUNG", "REFUND", "RÜCKZAHLUNG", "RUECKZAHLUNG"}, CatRefund},

	{[]string{"MIETE", "KALTMIETE", "WARMMIETE", "NEBENKOSTEN", "HAUSGELD", "RENT"}, CatHousing},
	{[]string{"STROM", "GAS", "WASSER", "ABWASSER", "MUELL", "MÜLL", "STADTWERKE", "VATTENFALL", "EON", "ENBW", "RWE", "TELEKOM", "VODAFONE", "1&1", "O2", "PYUR", "INTERNET"}, CatUtilities},
	{[]string{"VERSICHERUNG", "ALLIANZ", "HUK", "AXA", "ERGO", "DEVK", "DEBEKA", "GENERALI", "BARMER", "TK", "AOK", "DAK", "KRANKEN", "INSURANCE", "HAFTPFLICHT"}, CatInsurance},
	{[]string{"NETFLIX", "SPOTIFY", "AMAZON PRIME", "DISNEY", "APPLE.COM", "GITHUB", "ADOBE", "MICROSOFT", "ICLOUD", "DROPBOX", "YOUTUBE", "PATREON", "SUBSCRIPTION", "ABO "}, CatSubscriptions},

	{[]string{"REWE", "EDEKA", "LIDL", "ALDI", "PENNY", "NETTO", "KAUFLAND", "DM-DROGERIE", "ROSSMANN", "BIO COMPANY", "BUDNI", "MARKTKAUF", "TEGUT", "BACKEREI", "BÄCKEREI", "BAECKEREI"}, CatGroceries},
	{[]string{"RESTAURANT", "MCDONALD", "BURGER KING", "STARBUCKS", "PIZZA", "SUSHI", "DOENER", "DÖNER", "DINER", "LIEFERANDO", "WOLT", "UBER EATS", "TOO GOOD TO GO"}, CatDining},
	{[]string{"DB-VERTRIEB", "DEUTSCHE BAHN", "BVG", "MVV", "VRR", "RMV", "HVV", "9-EURO", "DEUTSCHLANDTICKET", "FLIXBUS", "FLIXTRAIN", "TANKE", "ESSO", "ARAL", "SHELL", "TOTAL", "JET", "BP-TANK", "ADAC", "UBER", "BOLT", "LIME ", "TIER"}, CatTransport},
	{[]string{"APOTHEKE", "PHARMACY", "ARZT", "ZAHNARZT", "DOCTOR", "PHYSIO", "OPTIKER", "FIELMANN"}, CatHealth},
	{[]string{"KINO", "CINEMA", "THEATER", "KONZERT", "TICKETMASTER", "EVENTIM", "STEAM ", "PLAYSTATION", "XBOX", "FITNESS", "FITX", "MCFIT", "URBAN SPORTS", "CLEVER FIT"}, CatEntertainment},
	{[]string{"AMAZON", "OTTO ", "ZALANDO", "MEDIAMARKT", "SATURN", "IKEA", "H&M", "ZARA", "C&A", "GALERIA", "BAUHAUS", "OBI ", "TOOM", "HORNBACH"}, CatShopping},

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
		for _, kw := range r.keywords {
			if strings.Contains(hay, kw) {
				return r.category
			}
		}
	}
	return CatOther
}

// ResolveCategory returns the user-override category if present, else the
// heuristic classification. The override must match a known Category; unknown
// override values fall through to the heuristic.
func ResolveCategory(override, txType, counterparty, reference string) Category {
	if override != "" {
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
		CatInternal, CatOther:
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
		CatInvestment, CatInternal, CatOther,
	}
}
