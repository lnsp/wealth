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
		CatAssetPurchase: BucketTransfer,
		CatLoanPrincipal: BucketTransfer,
		CatOther:         BucketVariable,
		CatDividend:      BucketIncome,
		CatFees:          BucketFixed,
		CatEducation:     BucketVariable,
		CatUnclassified:  BucketUnclassified,
	}
	for cat, want := range cases {
		if got := BucketOf(cat); got != want {
			t.Errorf("BucketOf(%q) = %q, want %q", cat, got, want)
		}
	}
}

// F1: an inflow that matches no rule is not income. Own-account transfers,
// Revolut top-ups and exchange legs, broker payouts and RSU sale wires are
// transfers; anything else stays unclassified until the user picks a category.
func TestClassifyInflows(t *testing.T) {
	cases := []struct {
		name, cp, ref string
		want          Category
	}{
		{"revolut apple pay top-up", "Apple Pay Top-Up by *1234", "TOPUP", CatInternal},
		{"revolut exchange leg", "Exchanged to USD", "EXCHANGE", CatInternal},
		{"tagesgeld deposit", "Max Mustermann", "Sparplan Tagesgeld", CatInternal},
		{"umbuchung", "Max Mustermann", "Umbuchung vom Sparkonto", CatInternal},
		{"morgan stanley rsu wire", "MORGAN STANLEY SMITH BARNEY", "WIRE 24405.76", CatInvestment},
		{"generic incoming wire", "JPMORGAN CHASE BANK", "INCOMING WIRE", CatInvestment},
		{"wirecard is not a wire", "WIRECARD BANK", "", CatUnclassified},
		{"schwab", "Charles Schwab & Co", "Stock plan proceeds", CatInvestment},
		{"broker withdrawal", "Scalable Capital GmbH", "Auszahlung", CatInvestment},
		{"unknown person", "Erika Musterfrau", "Danke!", CatUnclassified},
		{"merchant refund", "AMAZON.DE", "Gutschrift Bestellung", CatRefund},
		{"merchant refund keyword", "Some Shop", "Erstattung", CatRefund},
		{"tax refund", "Finanzamt Berlin", "Einkommensteuer 2025", CatRefund},
		{"lohnsteuer refund is not salary", "Finanzamt", "Lohnsteuer-Erstattung", CatRefund},
		{"salary", "ACME GmbH", "Gehalt März", CatSalary},
		{"pension", "Deutsche Rentenversicherung", "RENTE 11/2026", CatSalary},
		{"umbuchung gehalt stays a transfer", "Max Mustermann", "Umbuchung Gehalt", CatInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify("deposit", tc.cp, tc.ref); got != tc.want {
				t.Errorf("Classify(deposit, %q, %q) = %q, want %q", tc.cp, tc.ref, got, tc.want)
			}
		})
	}
	if b := BucketOf(CatUnclassified); b == BucketIncome {
		t.Errorf("unclassified inflows must not be income, bucket %q", b)
	}
}

func TestClassifyOwnIBAN(t *testing.T) {
	env := Env{OwnIBANs: []string{"de89 3704 0044 0532 0130 00"}}
	if got := ClassifyWith(env, "deposit", "Max Mustermann", "DE89370400440532013000"); got != CatInternal {
		t.Errorf("own IBAN deposit = %q, want internal", got)
	}
	if got := ClassifyWith(env, "withdrawal", "Max Mustermann", "IBAN DE89 3704 0044 0532 0130 00"); got != CatInternal {
		t.Errorf("own IBAN withdrawal = %q, want internal", got)
	}
	if got := ClassifyWith(Env{}, "deposit", "Max Mustermann", "DE89370400440532013000"); got != CatUnclassified {
		t.Errorf("IBAN not among own accounts = %q, want unclassified", got)
	}
}

// F2: payments to brokers and own savings accounts are transfers, not spending.
func TestClassifyOutflowsToInvestmentsAndOwnAccounts(t *testing.T) {
	cases := []struct {
		name, cp, ref string
		want          Category
	}{
		{"scalable sparplan", "Scalable Capital GmbH", "Sparplan", CatInvestment},
		{"trade republic", "Trade Republic Bank GmbH", "Einzahlung", CatInvestment},
		{"ing depot", "ING-DiBa AG", "ING Depot 123", CatInvestment},
		{"comdirect", "comdirect bank", "Uebertrag", CatInternal},
		{"flatex", "flatexDEGIRO Bank", "Einzahlung", CatInvestment},
		{"smartbroker", "DAB BNP Paribas", "Smartbroker Einzahlung", CatInvestment},
		{"dkb", "DKB AG", "Max Mustermann", CatInternal},
		{"tagesgeld", "Max Mustermann", "Umbuchung Tagesgeld", CatInternal},
		{"uebertrag", "Max Mustermann", "Übertrag auf Sparkonto", CatInternal},
		{"revolut top-up from checking", "Revolut Ltd", "Top-Up", CatInternal},
		{"mietkautionsdepot is not a broker", "Hausverwaltung", "Mietkautionsdepot Miete", CatHousing},
		{"depotgebuehr is not a transfer", "Bank", "Depotgebühr", CatOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify("withdrawal", tc.cp, tc.ref)
			if got != tc.want {
				t.Errorf("Classify(withdrawal, %q, %q) = %q, want %q", tc.cp, tc.ref, got, tc.want)
			}
			if tc.want == CatInvestment || tc.want == CatInternal {
				if b := BucketOf(got); b != BucketTransfer {
					t.Errorf("bucket = %q, want transfer", b)
				}
			}
		})
	}
}

// F3: bank fees and withheld tax on bank accounts are spending, not investment moves.
func TestClassifyFees(t *testing.T) {
	checking := Env{AccountType: "checking"}
	cases := []struct {
		name    string
		env     Env
		cp, ref string
		want    Category
		bucket  Bucket
	}{
		{"revolut plan fee", checking, "Plan subscription fee", "FEE", CatFees, BucketFixed},
		{"revolut tax row", checking, "Tax on interest", "TAX", CatTax, BucketVariable},
		{"sparkasse entgelt", Env{AccountType: "savings"}, "Sparkasse", "Entgeltabschluss", CatFees, BucketFixed},
		{"kapest", checking, "Kapitalertragsteuer", "", CatTax, BucketVariable},
		{"kirchensteuer", checking, "", "Kirchensteuer", CatTax, BucketVariable},
		{"soli", checking, "", "Soli", CatTax, BucketVariable},
		{"quellensteuer", checking, "", "US Quellensteuer", CatTax, BucketVariable},
		{"unknown account fee", Env{}, "Kontoführung", "", CatFees, BucketFixed},
		{"depot custody fee", Env{AccountType: "brokerage"}, "Depotgebühr", "", CatInvestment, BucketTransfer},
		{"tax on a depot is still tax", Env{AccountType: "brokerage"}, "KapESt", "", CatTax, BucketVariable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyWith(tc.env, "fee", tc.cp, tc.ref)
			if got != tc.want || BucketOf(got) != tc.bucket {
				t.Errorf("ClassifyWith(%+v, fee, %q, %q) = %q (%q), want %q (%q)", tc.env, tc.cp, tc.ref, got, BucketOf(got), tc.want, tc.bucket)
			}
		})
	}
	// The four-argument API behaves as an unknown account: a bank charge.
	if got := Classify("fee", "Plan subscription fee", "FEE"); got != CatFees {
		t.Errorf("Classify(fee) = %q, want fees", got)
	}
}

// F4: a withdrawal containing a refund word is a payment, never negative income.
func TestClassifyWithdrawalRefundWordsAreSpending(t *testing.T) {
	for _, tc := range []struct{ cp, ref string }{
		{"KfW", "Rückzahlung Studienkredit"},
		{"Max Mustermann", "Erstattung Auslagen"},
		{"Shop", "REFUND"},
		{"Bank", "Ruckzahlung"},
	} {
		if got := Classify("withdrawal", tc.cp, tc.ref); BucketOf(got) == BucketIncome {
			t.Errorf("Classify(withdrawal, %q, %q) = %q in the income bucket", tc.cp, tc.ref, got)
		}
	}
	if got := Classify("withdrawal", "KfW", "Rückzahlung Studienkredit"); got != CatOther {
		t.Errorf("KfW Rückzahlung = %q, want other (spending)", got)
	}
	if got := Classify("withdrawal", "Bank", "Sollzinsen 3. Quartal"); got != CatFees {
		t.Errorf("Sollzinsen = %q, want fees", got)
	}
}

func TestClassifyTypeShortCircuits(t *testing.T) {
	if got := Classify("dividend", "", ""); got != CatDividend || BucketOf(got) != BucketIncome {
		t.Errorf("dividend = %q, want dividend income", got)
	}
	if got := Classify("cash_transfer_in", "", ""); got != CatInternal {
		t.Errorf("cash_transfer_in = %q, want internal", got)
	}
}

// F9: categories the Spending tab relies on.
func TestClassifySpendingCategories(t *testing.T) {
	for _, tc := range []struct {
		cp, ref string
		want    Category
	}{
		{"STADTWERKE BERLIN", "Strom April", CatUtilities},
		{"ARD ZDF Deutschlandradio", "Rundfunkbeitrag", CatUtilities},
		{"Volkshochschule", "Kursgebühr", CatEducation},
		{"TÜV SÜD", "HU", CatTransport},
		{"Hauptzollamt", "KFZ-Steuer", CatTransport},
	} {
		if got := Classify("withdrawal", tc.cp, tc.ref); got != tc.want {
			t.Errorf("Classify(withdrawal, %q, %q) = %q, want %q", tc.cp, tc.ref, got, tc.want)
		}
	}
}

func TestResolveCategoryOverridesByDirection(t *testing.T) {
	// A spending override on money in is a refund, so spending never nets negative.
	if got := ResolveCategory(string(CatInsurance), "deposit", "Allianz", ""); got != CatRefund {
		t.Errorf("insurance override on a deposit = %q, want refund", got)
	}
	// The Sparkasse import hint "income" only applies to money in.
	if got := ResolveCategory("income", "deposit", "ACME", ""); got != CatSalary {
		t.Errorf("income hint on deposit = %q, want salary", got)
	}
	if got := ResolveCategory("income", "withdrawal", "Reinigungskraft", "Lohn Oktober"); BucketOf(got) == BucketIncome {
		t.Errorf("income hint on withdrawal = %q, must not be income", got)
	}
	if got := ResolveCategory("rent", "withdrawal", "Vermieter", ""); got != CatHousing {
		t.Errorf("rent hint on withdrawal = %q, want housing", got)
	}
	// Unclassified is a result, not something a user can assign.
	if IsKnown(CatUnclassified) {
		t.Error("unclassified must not be an accepted override")
	}
	if got := ResolveCategory(string(CatUnclassified), "withdrawal", "REWE", ""); got != CatGroceries {
		t.Errorf("unclassified override should fall through: got %q", got)
	}
}

func TestForCashDelta(t *testing.T) {
	cases := []struct {
		cat         Category
		delta       float64
		hasOverride bool
		want        Category
	}{
		{CatGroceries, 20, false, CatRefund},     // money back from a merchant
		{CatInsurance, 120, true, CatRefund},     // import-set category on money in
		{CatGroceries, -20, false, CatGroceries}, // ordinary spending
		{CatInterest, -4.12, false, CatFees},     // overdraft interest stored negative
		{CatSalary, -300, true, CatSalary},       // user says: salary paid back
		{CatInternal, 500, false, CatInternal},   // transfers keep their sign
		{CatUnclassified, 100, false, CatUnclassified},
	}
	for _, tc := range cases {
		if got := ForCashDelta(tc.cat, tc.delta, tc.hasOverride); got != tc.want {
			t.Errorf("ForCashDelta(%q, %.2f, %v) = %q, want %q", tc.cat, tc.delta, tc.hasOverride, got, tc.want)
		}
	}
}

func TestAllCategoriesAreKnown(t *testing.T) {
	for _, c := range AllCategories() {
		if !IsKnown(c) {
			t.Errorf("AllCategories lists %q but IsKnown rejects it", c)
		}
	}
}

func TestBankNameTransferExceptLoans(t *testing.T) {
	if got := Classify("withdrawal", "DKB AG", "Umbuchung"); got != CatInternal {
		t.Errorf("DKB transfer = %q, want internal", got)
	}
	if got := Classify("withdrawal", "DKB AG", "Darlehen 12345 Rate 11/2026"); BucketOf(got) == BucketTransfer {
		t.Errorf("DKB loan instalment = %q, must not be hidden as a transfer", got)
	}
}

func TestStateBenefitsAreIncome(t *testing.T) {
	for _, cp := range []string{"Familienkasse", "Bundesagentur fuer Arbeit Kindergeld", "Elterngeldstelle Elterngeld"} {
		got := Classify("deposit", cp, "")
		if got != CatBenefits || BucketOf(got) != BucketIncome {
			t.Errorf("Classify(deposit, %q) = %q (%s), want benefits in income", cp, got, BucketOf(got))
		}
	}
}
