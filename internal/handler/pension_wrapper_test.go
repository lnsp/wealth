package handler

import (
	"testing"

	"github.com/google/uuid"

	"github.com/lnsp/wealth/internal/analytics"
	db "github.com/lnsp/wealth/internal/database/generated"
)

// bAV, Riester and Rürup contracts are taxed on payout (§ 22 EStG); their
// trades must not reach any § 20 view, not only Anlage KAP.
func TestWithoutPensionWrappers(t *testing.T) {
	broker, bav := uuid.New(), uuid.New()
	accounts := []db.Account{
		{ID: broker, TaxTreatment: "taxable"},
		{ID: bav, TaxTreatment: "bav"},
	}
	txns := []analytics.TaxTransaction{
		{AccountID: broker.String(), Type: "sell", Amount: 1000},
		{AccountID: bav.String(), Type: "sell", Amount: 5000},
	}
	got := withoutPensionWrappers(txns, accounts)
	if len(got) != 1 || got[0].AccountID != broker.String() {
		t.Fatalf("got %+v, want only the brokerage row", got)
	}
	if len(txns) != 2 {
		t.Fatal("input slice must not be modified")
	}
}
