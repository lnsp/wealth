package analytics

import "testing"

func TestClassifyForAttribution_AllMigration021Types(t *testing.T) {
	// Every tx.type permitted by migration 021. If a new type is added to the
	// DB CHECK constraint, add it here and to ClassifyForAttribution.
	cases := []struct {
		name     string
		txType   string
		hasISIN  bool
		scope    Scope
		expected Bucket
	}{
		{"deposit, portfolio", "deposit", false, ScopePortfolio, BucketContribution},
		{"deposit, account", "deposit", false, ScopeAccount, BucketContribution},
		{"withdrawal, portfolio", "withdrawal", false, ScopePortfolio, BucketWithdrawal},
		{"withdrawal, account", "withdrawal", false, ScopeAccount, BucketWithdrawal},

		{"cash_transfer_in nets to zero portfolio-wide", "cash_transfer_in", false, ScopePortfolio, BucketIgnore},
		{"cash_transfer_in is a deposit for the receiving account", "cash_transfer_in", false, ScopeAccount, BucketContribution},
		{"cash_transfer_out nets to zero portfolio-wide", "cash_transfer_out", false, ScopePortfolio, BucketIgnore},
		{"cash_transfer_out is a withdrawal for the sending account", "cash_transfer_out", false, ScopeAccount, BucketWithdrawal},

		{"RSU vest (transfer + ISIN) is a contribution at FMV", "transfer", true, ScopePortfolio, BucketContribution},
		{"in-kind broker transfer + ISIN, per account", "transfer", true, ScopeAccount, BucketContribution},
		{"legacy cash transfer (no ISIN) is a withdrawal", "transfer", false, ScopePortfolio, BucketWithdrawal},
		{"legacy cash transfer (no ISIN), per account", "transfer", false, ScopeAccount, BucketWithdrawal},

		{"transfer_out is the matching half — ignore portfolio-wide", "transfer_out", true, ScopePortfolio, BucketIgnore},
		{"transfer_out is the matching half — ignore per account too", "transfer_out", true, ScopeAccount, BucketIgnore},

		{"dividend", "dividend", false, ScopePortfolio, BucketDividend},
		{"interest", "interest", false, ScopePortfolio, BucketInterest},
		{"fee", "fee", false, ScopePortfolio, BucketFee},
		{"tax", "tax", false, ScopePortfolio, BucketTax},

		{"buy is intra-portfolio", "buy", true, ScopePortfolio, BucketIgnore},
		{"sell is intra-portfolio", "sell", true, ScopePortfolio, BucketIgnore},
		{"savings_plan execution is intra-portfolio", "savings_plan", true, ScopePortfolio, BucketIgnore},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyForAttribution(tc.txType, tc.hasISIN, tc.scope)
			if got != tc.expected {
				t.Fatalf("ClassifyForAttribution(%q, hasISIN=%v, scope=%v) = %v, want %v",
					tc.txType, tc.hasISIN, tc.scope, got, tc.expected)
			}
		})
	}
}

func TestClassifyForAttribution_UnknownTypeIsIgnored(t *testing.T) {
	// Unknown types fall into Ignore today. If you change this contract,
	// audit every caller that uses ClassifyForAttribution in a residual
	// computation (e.g. HandleWealthWaterfall) — silent Ignore there means
	// the residual absorbs the unclassified amount.
	if got := ClassifyForAttribution("brand_new_type", false, ScopePortfolio); got != BucketIgnore {
		t.Fatalf("unknown type should default to Ignore, got %v", got)
	}
}

func TestClassifyForAttribution_MorganStanleyAutoWire(t *testing.T) {
	// The MS importer emits a paired (sell, withdrawal) for each RSU sale to
	// auto-wire the proceeds out of the cash-neutral MS account. Both halves
	// must classify so the waterfall doesn't double-count.
	if got := ClassifyForAttribution("sell", true, ScopePortfolio); got != BucketIgnore {
		t.Fatalf("MS auto-wire: sell should be Ignore (intra-portfolio), got %v", got)
	}
	if got := ClassifyForAttribution("withdrawal", false, ScopePortfolio); got != BucketWithdrawal {
		t.Fatalf("MS auto-wire: paired withdrawal should classify as Withdrawal, got %v", got)
	}
	// Net of (vest contribution at FMV + sale Ignore + withdrawal at proceeds)
	// should equal (FMV − proceeds) appearing as the residual market return.
	// That residual is captured in the waterfall once A.2 lands.
}
