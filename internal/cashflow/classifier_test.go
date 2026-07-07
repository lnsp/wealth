package cashflow

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		name         string
		txType       string
		counterparty string
		reference    string
		want         Category
	}{
		{"salary", "deposit", "ACME GMBH", "GEHALT MAERZ 2026", CatSalary},
		{"interest type wins", "interest", "Sparkasse", "Gutschrift Zinsen", CatInterest},
		{"rent", "withdrawal", "WOHNUNG VERWALTUNG GMBH", "Miete April", CatHousing},
		{"groceries", "withdrawal", "REWE Berlin", "EC-Kartenzahlung", CatGroceries},
		{"subscription Netflix", "withdrawal", "NETFLIX.COM", "Monatsabo", CatSubscriptions},
		{"transport DB", "withdrawal", "DB-VERTRIEB GMBH", "Ticket Berlin-Munich", CatTransport},
		{"insurance Allianz", "withdrawal", "ALLIANZ Lebensversicherung", "", CatInsurance},
		{"unmatched falls back to other", "withdrawal", "RANDOM SHOP XYZ", "purchase", CatOther},
		{"internal transfer", "cash_transfer_in", "", "", CatInternal},
		{"investment buy", "buy", "Scalable", "Sparplan ETF", CatInvestment},
		{"tax type", "tax", "Finanzamt", "Vorauszahlung", CatTax},
		{"case-insensitive", "withdrawal", "rewe city", "", CatGroceries},
		{"keyword inside reference", "withdrawal", "SEPA Lastschrift", "Spotify Family", CatSubscriptions},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.txType, tc.counterparty, tc.reference)
			if got != tc.want {
				t.Errorf("Classify(%q, %q, %q) = %q, want %q", tc.txType, tc.counterparty, tc.reference, got, tc.want)
			}
		})
	}
}

func TestResolveCategoryHonorsOverride(t *testing.T) {
	// Heuristic would say groceries; user override pins it to entertainment.
	got := ResolveCategory(string(CatEntertainment), "withdrawal", "REWE", "")
	if got != CatEntertainment {
		t.Errorf("override ignored: got %q, want %q", got, CatEntertainment)
	}

	// Unknown override falls through to heuristic.
	got = ResolveCategory("bogus", "withdrawal", "REWE", "")
	if got != CatGroceries {
		t.Errorf("unknown override should fall through: got %q, want %q", got, CatGroceries)
	}

	// Empty override uses heuristic.
	got = ResolveCategory("", "withdrawal", "NETFLIX", "")
	if got != CatSubscriptions {
		t.Errorf("empty override should use heuristic: got %q, want %q", got, CatSubscriptions)
	}
}

func TestBucketOf(t *testing.T) {
	cases := map[Category]Bucket{
		CatSalary:        BucketIncome,
		CatHousing:       BucketFixed,
		CatSubscriptions: BucketFixed,
		CatGroceries:     BucketVariable,
		CatShopping:      BucketVariable,
		CatInternal:      BucketTransfer,
		CatInvestment:    BucketTransfer,
		CatOther:         BucketVariable,
	}
	for cat, want := range cases {
		if got := BucketOf(cat); got != want {
			t.Errorf("BucketOf(%q) = %q, want %q", cat, got, want)
		}
	}
}
