package analytics

// Bucket categorises a transaction for wealth-attribution purposes.
//
// The canonical question is: "where did the change in net worth come from?"
// Every transaction either moves money in/out from outside the user's
// portfolio (Contribution/Withdrawal), realises portfolio income
// (Dividend/Interest), reduces wealth (Fee/Tax), is purely a position
// rearrangement that nets to zero (Ignore), or is an in-kind change in
// holdings that should be valued at FMV (MarketEvent — reserved for future
// use; today only Contribution covers in-kind grants).
type Bucket int

const (
	BucketIgnore Bucket = iota
	BucketContribution
	BucketWithdrawal
	BucketDividend
	BucketInterest
	BucketFee
	BucketTax
)

// Scope distinguishes per-account from portfolio-wide attribution. Internal
// transfers between two of the user's own accounts are real cash flows from
// the perspective of one account but a wash at the portfolio level.
type Scope int

const (
	ScopePortfolio Scope = iota
	ScopeAccount
)

// ClassifyForAttribution maps a transaction type to an attribution bucket.
//
// All thirteen tx.type values permitted by migration 021 are handled. The
// `hasISIN` parameter disambiguates `transfer`, which historically carries
// both meanings:
//   - transfer + ISIN  → in-kind security grant (e.g. RSU vest), valued at
//     amount = FMV at vest → Contribution
//   - transfer − ISIN  → legacy cash transfer out → Withdrawal
//
// `transfer_out` is the matching half of an in-kind move between two of the
// user's accounts. Portfolio-wide it nets to zero with the `transfer` row on
// the receiving account, so we Ignore it. Callers needing per-account
// attribution must look at `transfer` with ISIN inside ScopeAccount.
func ClassifyForAttribution(txType string, hasISIN bool, scope Scope) Bucket {
	switch txType {
	case "deposit":
		return BucketContribution
	case "withdrawal":
		return BucketWithdrawal
	case "cash_transfer_in":
		if scope == ScopeAccount {
			return BucketContribution
		}
		return BucketIgnore
	case "cash_transfer_out":
		if scope == ScopeAccount {
			return BucketWithdrawal
		}
		return BucketIgnore
	case "transfer":
		if hasISIN {
			return BucketContribution
		}
		return BucketWithdrawal
	case "transfer_out":
		return BucketIgnore
	case "dividend":
		return BucketDividend
	case "interest":
		return BucketInterest
	case "fee":
		return BucketFee
	case "tax":
		return BucketTax
	case "buy", "sell", "savings_plan":
		// Buys/sells/sparplan executions move cash into securities or vice
		// versa within the portfolio — they don't change net worth at the
		// instant of execution (ignoring fees/taxes, which are billed
		// separately on the same row via tx.Fee / tx.Tax).
		return BucketIgnore
	}
	// Unknown type. Returning Ignore here would silently misclassify any
	// future tx.type into the residual; callers that care should detect this
	// explicitly. We keep this conservative and return Ignore, but reserve
	// the right to make this a panic in tests.
	return BucketIgnore
}
