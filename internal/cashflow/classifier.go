// Package cashflow classifies cash transactions into spending categories.
//
// The classifier is intentionally simple: deterministic keyword matching over
// counterparty + reference text, with the existing transactions.category
// column acting as a user override (taking precedence over the heuristic).
//
// Categories are German-tailored — payroll keywords ("GEHALT", "LOHN"),
// landlord patterns ("MIETE", "KALTMIETE"), supermarkets (REWE/Edeka/
// Lidl/Aldi), brokers (Scalable, Trade Republic), and so on.
//
// Direction matters. The transaction type carries it (imports store amounts
// as magnitudes): a `deposit` is money in, a `withdrawal` money out.
//   - Money out that matches no rule lands in `other` and counts as spending.
//   - Money in that matches a merchant category is a refund from that merchant.
//   - Money in that matches no rule lands in `unclassified`: it is neither
//     income nor spending until the user assigns a category, because an
//     unknown inflow is as likely an own-account transfer or a broker payout
//     as it is income.
package cashflow

import (
	"strings"
	"unicode"
)

// Bucket is the top-level cashflow class used to compute Net Surplus.
type Bucket string

const (
	BucketIncome   Bucket = "income"   // payroll, interest, dividends paid out, refunds
	BucketFixed    Bucket = "fixed"    // rent, utilities, insurance, subscriptions, bank fees
	BucketVariable Bucket = "variable" // groceries, dining, transport, shopping, etc.
	BucketTransfer Bucket = "transfer" // internal moves (savings, brokerage funding) — excluded from spend
	// BucketUnclassified holds inflows no rule recognised. They are excluded
	// from both income and spending and shown separately.
	BucketUnclassified Bucket = "unclassified"
)

// Category is the finer-grained label shown in the breakdown chart.
type Category string

const (
	CatSalary        Category = "salary"
	CatInterest      Category = "interest"
	CatDividend      Category = "dividend"
	CatRefund        Category = "refund"
	CatHousing       Category = "housing"
	CatUtilities     Category = "utilities"
	CatInsurance     Category = "insurance"
	CatSubscriptions Category = "subscriptions"
	// CatFees is bank charges: account and card fees, overdraft interest.
	CatFees          Category = "fees"
	CatGroceries     Category = "groceries"
	CatDining        Category = "dining"
	CatTransport     Category = "transport"
	CatHealth        Category = "health"
	CatEntertainment Category = "entertainment"
	CatShopping      Category = "shopping"
	CatEducation     Category = "education"
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
	// CatUnclassified is an inflow no rule recognised. It is a classifier
	// result only, not a valid user override (see IsKnown).
	CatUnclassified Category = "unclassified"
)

// BucketOf maps a Category to its top-level Bucket.
func BucketOf(c Category) Bucket {
	switch c {
	case CatSalary, CatInterest, CatDividend, CatRefund:
		return BucketIncome
	case CatHousing, CatUtilities, CatInsurance, CatSubscriptions, CatFees:
		return BucketFixed
	case CatInvestment, CatInternal, CatAssetPurchase, CatLoanPayment, CatLoanPrincipal:
		return BucketTransfer
	case CatUnclassified:
		return BucketUnclassified
	default:
		return BucketVariable
	}
}

// Env carries facts about a transaction's account that the keyword rules
// cannot see. The zero value is valid and means "unknown".
type Env struct {
	// AccountType is accounts.type of the row's account ("checking",
	// "savings", "brokerage", ...). It decides whether a `fee` row is a bank
	// charge (spending) or an investment cost.
	AccountType string
	// OwnIBANs are the IBANs of the user's accounts, in any spacing or case.
	// A transaction whose text names one of them is an own-account transfer.
	OwnIBANs []string
}

// wordBoundedKeywords lists short or ambiguous uppercase keywords that must match
// on non-alphanumeric word boundaries (preventing e.g. "TK" inside "MARKTKAUF"
// or "KREDITKARTE", "GAS" inside "GASTHAUS", "TAX" inside "TAXI", "TIER" inside
// "TIERARZT", "UBER" inside "HUBER", "DEPOT" inside "MIETKAUTIONSDEPOT" and
// "WIRE" inside "WIRECARD").
var wordBoundedKeywords = map[string]bool{
	"TK": true, "AOK": true, "DAK": true, "HUK": true, "AXA": true, "DEVK": true, "ERGO": true,
	"GAS": true, "EON": true, "RWE": true, "O2": true, "RENT": true, "TAX": true,
	"TIER": true, "UBER": true, "JET": true, "TOTAL": true, "ESSO": true, "ARAL": true,
	"BVG": true, "MVV": true, "VRR": true, "RMV": true, "HVV": true, "ADAC": true,
	"BOLT": true, "LIME": true, "ABO": true, "OTTO": true, "OBI": true, "TOOM": true,
	"C&A": true, "H&M": true, "ZARA": true, "FITX": true, "STEAM": true, "XBOX": true,
	"DEPOT": true, "DKB": true, "REVOLUT": true, "EXCHANGE": true, "WIRE": true,
	"SCHWAB": true, "SOLI": true, "KIST": true, "VHS": true, "GEZ": true, "KFZ": true, "TÜV": true,
	"RENTE": true,
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

func matchesAny(hay string, kws []string) bool {
	for _, kw := range kws {
		if matchesKeyword(hay, kw) {
			return true
		}
	}
	return false
}

// direction limits a rule to money in or money out.
type direction int

const (
	anyDir  direction = iota
	inOnly            // deposits (and rows of unknown direction)
	outOnly           // withdrawals (and rows of unknown direction)
)

// flow is the direction a transaction type implies.
type flow int

const (
	flowUnknown flow = iota
	flowIn
	flowOut
)

func flowOf(txType string) flow {
	switch txType {
	case "deposit", "interest", "dividend", "sell", "cash_transfer_in":
		return flowIn
	case "withdrawal", "fee", "tax", "buy", "savings_plan", "cash_transfer_out", "transfer_out":
		return flowOut
	}
	return flowUnknown
}

// rule is one keyword list; the first matching rule wins.
type rule struct {
	keywords []string
	category Category
	dir      direction
}

// rules are matched in order against UPPER(counterparty + " " + reference).
//
// Own-account and tax words come first so "Umbuchung Gehalt" stays a transfer
// and "Lohnsteuer-Erstattung" isn't read as salary. Salary and interest only
// apply to money in, bank-fee words only to money out. Refund words only apply
// to money in: a withdrawal "Rückzahlung Studienkredit" is a payment, not
// negative income.
var rules = []rule{
	// Own-account transfers: savings accounts, Revolut top-ups and currency
	// exchange legs, transfers between the user's banks.
	{[]string{"UMBUCHUNG", "ÜBERTRAG", "UEBERTRAG", "EIGENES KONTO", "EIGENE KONTEN", "EIGENKONTO",
		"INTERNAL TRANSFER", "OWN ACCOUNT", "TAGESGELD", "FESTGELD", "SPARKONTO", "SPARBUCH",
		"TOPUP", "TOP-UP", "EXCHANGED TO", "EXCHANGED FROM", "EXCHANGE", "REVOLUT", "DKB"}, CatInternal, anyDir},
	// Income tax payments and refunds. Ahead of salary because "LOHN" is in "LOHNSTEUER".
	{[]string{"FINANZAMT", "LOHNSTEUER", "EINKOMMENSTEUER", "EINKOMMENSSTEUER", "STEUERERSTATTUNG"}, CatTax, anyDir},

	// Pay and pensions ("RENTE" but not "RENTENVERSICHERUNG").
	{[]string{"GEHALT", "LOHN", "SALARY", "PAYROLL", "BEZÜGE", "BEZUEGE", "RENTE"}, CatSalary, inOnly},
	{[]string{"ZINSEN", "INTEREST"}, CatInterest, inOnly},

	// Brokers, savings plans and RSU sale proceeds: money moved into or out of
	// investments, not income or spending.
	{[]string{"SCALABLE", "TRADE REPUBLIC", "TRADEREPUBLIC", "COMDIRECT", "FLATEX", "SMARTBROKER",
		"ONVISTA", "CONSORSBANK", "FINANZEN.NET ZERO", "JUSTTRADE", "JUST TRADE", "BAADER BANK",
		"DEGIRO", "INTERACTIVE BROKERS", "MORGAN STANLEY", "SCHWAB", "E*TRADE", "ETRADE", "E-TRADE",
		"EQUATEPLUS", "COMPUTERSHARE", "SHAREWORKS", "SPARPLAN", "WERTPAPIER", "DEPOT"}, CatInvestment, anyDir},

	{[]string{"ERSTATTUNG", "REFUND", "RÜCKZAHLUNG", "RUECKZAHLUNG", "CASHBACK"}, CatRefund, inOnly},
	// An incoming wire with no payroll keyword is almost always a broker payout
	// (RSU sale proceeds), not earned income.
	{[]string{"WIRE"}, CatInvestment, inOnly},

	{[]string{"KONTOFÜHRUNG", "KONTOFUEHRUNG", "KONTOENTGELT", "ENTGELTABSCHLUSS", "KARTENGEBÜHR",
		"KARTENGEBUEHR", "BANKGEBÜHR", "BANKGEBUEHR", "SOLLZINS", "DISPOZINS", "ÜBERZIEHUNGSZINS",
		"UEBERZIEHUNGSZINS"}, CatFees, outOnly},

	{[]string{"MIETE", "KALTMIETE", "WARMMIETE", "NEBENKOSTEN", "HAUSGELD", "RENT"}, CatHousing, anyDir},
	{[]string{"STROM", "ERDGAS", "GASAG", "GASWERK", "GASVERSORG", "GAS", "WASSER", "ABWASSER", "MUELL", "MÜLL", "STADTWERKE", "VATTENFALL", "EON", "ENBW", "RWE", "TELEKOM", "VODAFONE", "1&1", "O2", "PYUR", "INTERNET", "RUNDFUNK", "GEZ", "HEIZUNG", "FERNWÄRME", "FERNWAERME"}, CatUtilities, anyDir},
	{[]string{"VERSICHERUNG", "ALLIANZ", "HUK", "AXA", "ERGO", "DEVK", "DEBEKA", "GENERALI", "BARMER", "TECHNIKER KRANKENKASSE", "TK", "AOK", "DAK", "KRANKENKASSE", "KRANKENVERSICHERUNG", "INSURANCE", "HAFTPFLICHT"}, CatInsurance, anyDir},
	{[]string{"NETFLIX", "SPOTIFY", "AMAZON PRIME", "DISNEY", "APPLE.COM", "GITHUB", "ADOBE", "MICROSOFT", "ICLOUD", "DROPBOX", "YOUTUBE", "PATREON", "SUBSCRIPTION", "MONATSABO", "JAHRESABO", "ABO"}, CatSubscriptions, anyDir},

	{[]string{"REWE", "EDEKA", "LIDL", "ALDI", "PENNY", "NETTO", "KAUFLAND", "DM-DROGERIE", "ROSSMANN", "BIO COMPANY", "BUDNI", "MARKTKAUF", "TEGUT", "BACKEREI", "BÄCKEREI", "BAECKEREI"}, CatGroceries, anyDir},
	{[]string{"RESTAURANT", "GASTHAUS", "GASTSTÄTTE", "GASTSTAETTE", "MCDONALD", "BURGER KING", "STARBUCKS", "PIZZA", "SUSHI", "DOENER", "DÖNER", "DINER", "LIEFERANDO", "WOLT", "UBER EATS", "TOO GOOD TO GO"}, CatDining, anyDir},
	{[]string{"APOTHEKE", "PHARMACY", "TIERARZT", "ZAHNARZT", "HAUSARZT", "FACHARZT", "ARZTPRAXIS", "DOCTOR", "PHYSIO", "OPTIKER", "FIELMANN"}, CatHealth, anyDir},
	{[]string{"DB-VERTRIEB", "DEUTSCHE BAHN", "BVG", "MVV", "VRR", "RMV", "HVV", "9-EURO", "DEUTSCHLANDTICKET", "FLIXBUS", "FLIXTRAIN", "TAXI", "TANKE", "TANKSTELLE", "ESSO", "ARAL", "SHELL", "TOTAL", "JET", "BP-TANK", "ADAC", "UBER", "BOLT", "LIME", "TIER", "KFZ", "TÜV"}, CatTransport, anyDir},
	{[]string{"KINO", "CINEMA", "THEATER", "KONZERT", "TICKETMASTER", "EVENTIM", "STEAM", "PLAYSTATION", "XBOX", "FITNESS", "FITX", "MCFIT", "URBAN SPORTS", "CLEVER FIT"}, CatEntertainment, anyDir},
	{[]string{"AMAZON", "OTTO", "ZALANDO", "MEDIAMARKT", "SATURN", "IKEA", "H&M", "ZARA", "C&A", "GALERIA", "BAUHAUS", "OBI", "TOOM", "HORNBACH"}, CatShopping, anyDir},
	{[]string{"SCHULE", "UNIVERSITÄT", "UNIVERSITAET", "HOCHSCHULE", "STUDIENGEBÜHR", "STUDIENGEBUEHR", "SEMESTERBEITRAG", "VHS", "UDEMY", "COURSERA"}, CatEducation, anyDir},

	{[]string{"STEUER", "TAX"}, CatTax, anyDir},
}

// taxFeeKeywords mark a `fee` row as withheld tax rather than a charge.
var taxFeeKeywords = []string{
	"KAPEST", "KAPITALERTRAG", "ABGELTUNG", "KIRCHENSTEUER", "KIST", "SOLIDARITÄTSZUSCHLAG",
	"SOLIDARITAETSZUSCHLAG", "SOLI", "QUELLENSTEUER", "WITHHOLDING", "STEUER", "TAX",
}

// Classify returns the inferred Category for a transaction. `txType` is the
// existing transactions.type (deposit, withdrawal, etc.) — used to short-
// circuit obvious cases. `counterparty` and `reference` are matched against
// the keyword rules. Empty strings are tolerated. It is ClassifyWith with an
// unknown account.
func Classify(txType, counterparty, reference string) Category {
	return ClassifyWith(Env{}, txType, counterparty, reference)
}

// ClassifyWith is Classify with facts about the transaction's account.
func ClassifyWith(env Env, txType, counterparty, reference string) Category {
	hay := strings.ToUpper(counterparty + " " + reference)
	switch txType {
	case "interest":
		return CatInterest
	case "dividend":
		return CatDividend
	case "tax":
		return CatTax
	case "fee":
		// Parsers send bank charges and withheld tax (Revolut fee/tax rows,
		// Sparkasse Entgelt and negative interest, Scalable KapESt) to `fee`.
		if matchesAny(hay, taxFeeKeywords) {
			return CatTax
		}
		if isInvestmentAccount(env.AccountType) {
			return CatInvestment // custody or order costs on a depot
		}
		return CatFees
	case "transfer", "transfer_out", "cash_transfer_in", "cash_transfer_out":
		return CatInternal
	case "buy", "sell", "savings_plan":
		return CatInvestment
	}

	if namesOwnAccount(hay, env.OwnIBANs) {
		return CatInternal
	}

	f := flowOf(txType)
	cat := CatOther
	matched := false
	for _, r := range rules {
		if (r.dir == inOnly && f == flowOut) || (r.dir == outOnly && f == flowIn) {
			continue
		}
		if matchesAny(hay, r.keywords) {
			cat, matched = r.category, true
			break
		}
	}
	if !matched && hasWordBoundaryMatch(hay, "ARZT") {
		cat, matched = CatHealth, true
	}
	if f == flowIn {
		if !matched {
			return CatUnclassified
		}
		return inflowCategory(cat)
	}
	return cat
}

// inflowCategory turns a spending category on money in into a refund from
// that merchant, so spending categories never go negative.
func inflowCategory(c Category) Category {
	if b := BucketOf(c); b == BucketFixed || b == BucketVariable {
		return CatRefund
	}
	return c
}

// ForCashDelta adjusts a resolved category to the direction the money moved,
// for callers that know the signed cash effect (`delta`, positive = money in):
//   - money in under a spending category is a refund, so spending never nets
//     negative (e.g. a loan instalment refunded, a legacy signed row);
//   - money out under an income category with no override is a charge, e.g.
//     overdraft interest imported as a negative `interest` row.
//
// A user override on money out (salary paid back) is kept.
func ForCashDelta(c Category, delta float64, hasOverride bool) Category {
	b := BucketOf(c)
	switch {
	case delta > 0 && (b == BucketFixed || b == BucketVariable):
		return CatRefund
	case delta < 0 && b == BucketIncome && !hasOverride:
		return CatFees
	}
	return c
}

func isInvestmentAccount(accountType string) bool {
	switch accountType {
	case "brokerage", "crypto", "pension", "precious_metals":
		return true
	}
	return false
}

// namesOwnAccount reports whether the text contains one of the user's IBANs.
func namesOwnAccount(hay string, ibans []string) bool {
	if len(ibans) == 0 {
		return false
	}
	compact := strings.ReplaceAll(hay, " ", "")
	for _, iban := range ibans {
		iban = strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(iban), " ", ""))
		if len(iban) >= 15 && strings.Contains(compact, iban) { // shortest IBANs (Norway) have 15 characters
			return true
		}
	}
	return false
}

// ResolveCategory returns the user-override category if present, else the
// heuristic classification. The override must match a known Category; unknown
// override values fall through to the heuristic. It is ResolveCategoryWith
// with an unknown account.
func ResolveCategory(override, txType, counterparty, reference string) Category {
	return ResolveCategoryWith(Env{}, override, txType, counterparty, reference)
}

// ResolveCategoryWith is ResolveCategory with facts about the account.
//
// A spending-category override on money in resolves to CatRefund, the same as
// the heuristic does, so a spending category never nets negative.
func ResolveCategoryWith(env Env, override, txType, counterparty, reference string) Category {
	if override != "" {
		var cat Category
		switch strings.ToLower(strings.TrimSpace(override)) {
		case "income":
			// Import-time hint from the Sparkasse booking text ("Lohn/Gehalt");
			// it says nothing about payments going out.
			if flowOf(txType) != flowOut {
				cat = CatSalary
			}
		case "rent":
			cat = CatHousing
		default:
			if known := Category(override); IsKnown(known) {
				cat = known
			}
		}
		if cat != "" {
			if flowOf(txType) == flowIn {
				return inflowCategory(cat)
			}
			return cat
		}
	}
	return ClassifyWith(env, txType, counterparty, reference)
}

// IsKnown reports whether c is one of the predefined categories a user can
// assign. CatUnclassified is deliberately not one of them.
func IsKnown(c Category) bool {
	switch c {
	case CatSalary, CatInterest, CatDividend, CatRefund,
		CatHousing, CatUtilities, CatInsurance, CatSubscriptions, CatFees,
		CatGroceries, CatDining, CatTransport, CatHealth,
		CatEntertainment, CatShopping, CatEducation, CatTax, CatInvestment,
		CatInternal, CatAssetPurchase, CatLoanPayment, CatLoanPrincipal, CatOther:
		return true
	}
	return false
}

// AllCategories returns every category a user can assign, in display order.
func AllCategories() []Category {
	return []Category{
		CatSalary, CatInterest, CatDividend, CatRefund,
		CatHousing, CatUtilities, CatInsurance, CatSubscriptions, CatFees,
		CatGroceries, CatDining, CatTransport, CatHealth,
		CatEntertainment, CatShopping, CatEducation, CatTax,
		CatInvestment, CatInternal, CatAssetPurchase, CatLoanPayment, CatLoanPrincipal, CatOther,
	}
}
