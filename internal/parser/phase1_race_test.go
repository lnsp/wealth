package parser

import (
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestScalableParser_NoStickyAccountTypeAndRaceSafe(t *testing.T) {
	savingsCSV := []byte("date;time;status;reference;description;assetType;type;isin;shares;price;amount;fee;tax;currency;account\n" +
		"2026-01-10;10:00:00;Executed;REF1;Interest;Cash;Interest;;;;12,50;0,00;0,00;EUR;savings\n")
	brokerageCSV := []byte("date;time;status;reference;description;assetType;type;isin;shares;price;amount;fee;tax;currency\n" +
		"2026-01-11;10:00:00;Executed;REF2;MSCI World;Security;Buy;IE00B4L5Y983;2;100,00;-200,00;0,99;0,00;EUR\n")

	accID := uuid.New()
	_, _, res1, err := ParseCSV(savingsCSV, accID)
	if err != nil {
		t.Fatalf("savings parse failed: %v", err)
	}
	if res1.AccountType != "savings" {
		t.Fatalf("expected AccountType=savings, got %q", res1.AccountType)
	}

	_, _, res2, err := ParseCSV(brokerageCSV, accID)
	if err != nil {
		t.Fatalf("brokerage parse failed: %v", err)
	}
	if res2.AccountType != "" {
		t.Fatalf("expected AccountType to be empty after savings import, got %q", res2.AccountType)
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _, _, _ = ParseCSV(savingsCSV, accID)
		}()
		go func() {
			defer wg.Done()
			_, _, _, _ = ParseCSV(brokerageCSV, accID)
		}()
	}
	wg.Wait()
}
