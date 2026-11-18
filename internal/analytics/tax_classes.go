package analytics

// Shared German tax classification helpers. Several handlers (tax summary,
// loss pots, Anlage KAP, FSA status, sell simulator, planning) need the same
// answers to "which pot does this belong to" and "which rate applies", so
// they live here rather than being re-derived per call site.

// IsShareAssetClass reports whether a security is an Aktie in the sense of
// §20 Abs. 6 S. 4 EStG. Only losses from selling shares are ring-fenced into
// the Aktienverlusttopf; losses on funds/ETFs, bonds and derivatives go to the
// general pot and may offset any capital income.
func IsShareAssetClass(assetClass string) bool {
	return assetClass == "stock"
}

// IsPrivateSaleAssetClass reports whether gains on the asset fall under
// private sales (§23 Abs. 1 Nr. 2 EStG) rather than capital income (§20):
// crypto and physically backed commodities (BFH VIII R 4/15 for Xetra-Gold
// style ETCs). These are tax-free after a one-year holding period, otherwise
// taxed at the personal income-tax rate with a Freigrenze, and must never be
// fed into Abgeltungsteuer, the Sparer-Pauschbetrag or Anlage KAP.
func IsPrivateSaleAssetClass(assetClass string) bool {
	return assetClass == "crypto" || assetClass == "commodity"
}

// PrivateSaleFreigrenze returns the §23 Abs. 3 S. 5 EStG exemption limit for
// a tax year: 600 € up to 2023, 1,000 € from 2024. It is a Freigrenze, not a
// Freibetrag — once the year's net private-sale gain reaches it, the whole
// amount is taxable.
func PrivateSaleFreigrenze(year int) float64 {
	if year <= 2023 {
		return 600
	}
	return 1000
}

// SparerPauschbetragForYear returns the saver's allowance (§20 Abs. 9 EStG)
// for a tax year: 801 € (1,602 € joint) up to 2022, 1,000 € (2,000 € joint)
// from 2023 (Jahressteuergesetz 2022).
func SparerPauschbetragForYear(year int, joint bool) float64 {
	base := Sparerpauschbetrag
	if year > 0 && year <= 2022 {
		base = 801
	}
	if joint {
		return 2 * base
	}
	return base
}

// CapitalIncomeTaxRate returns the combined Abgeltungsteuer + Soli +
// Kirchensteuer rate on a euro of taxable capital income. Kirchensteuer is
// deductible from the Abgeltungsteuer base (§32d Abs. 1 S. 3-5 EStG), so the
// tax is e/(4+k) rather than 25 % + k·25 %:
//
//	k = 0    → 26.375 %
//	k = 0.08 → 27.8186 %
//	k = 0.09 → 27.9951 %
//
// churchTaxRate is clamped to [0, 0.09].
func CapitalIncomeTaxRate(churchTaxRate float64) float64 {
	if churchTaxRate < 0 {
		churchTaxRate = 0
	}
	if churchTaxRate > 0.09 {
		churchTaxRate = 0.09
	}
	kapESt := 1 / (4 + churchTaxRate)
	return kapESt * (1 + Solidaritaetszuschlag + churchTaxRate)
}
