package market

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestECBClient_FetchDailyRates(t *testing.T) {
	tests := []struct {
		name       string
		response   string
		statusCode int
		wantErr    bool
		wantLen    int
		wantDate   time.Time
	}{
		{
			name: "valid response with two currencies",
			response: `<?xml version="1.0" encoding="UTF-8"?>
<gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref">
  <Cube>
    <Cube time="2026-03-24">
      <Cube currency="USD" rate="1.0834"/>
      <Cube currency="GBP" rate="0.8321"/>
    </Cube>
  </Cube>
</gesmes:Envelope>`,
			statusCode: http.StatusOK,
			wantLen:    2,
			wantDate:   time.Date(2026, 3, 24, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "single currency",
			response: `<?xml version="1.0" encoding="UTF-8"?>
<gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref">
  <Cube>
    <Cube time="2026-01-15">
      <Cube currency="CHF" rate="0.9456"/>
    </Cube>
  </Cube>
</gesmes:Envelope>`,
			statusCode: http.StatusOK,
			wantLen:    1,
			wantDate:   time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
		},
		{
			name:       "server error",
			response:   "Internal Server Error",
			statusCode: http.StatusInternalServerError,
			wantErr:    true,
		},
		{
			name: "empty cube",
			response: `<?xml version="1.0" encoding="UTF-8"?>
<gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref">
  <Cube></Cube>
</gesmes:Envelope>`,
			statusCode: http.StatusOK,
			wantErr:    true,
		},
		{
			name: "invalid rate skipped",
			response: `<?xml version="1.0" encoding="UTF-8"?>
<gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref">
  <Cube>
    <Cube time="2026-03-24">
      <Cube currency="USD" rate="not_a_number"/>
      <Cube currency="GBP" rate="0.8321"/>
    </Cube>
  </Cube>
</gesmes:Envelope>`,
			statusCode: http.StatusOK,
			wantLen:    1,
			wantDate:   time.Date(2026, 3, 24, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.response))
			}))
			defer server.Close()

			client := &ECBClient{httpClient: server.Client()}
			rates, date, err := client.fetchRates(context.Background(), server.URL)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(rates) != tt.wantLen {
				t.Errorf("got %d rates, want %d", len(rates), tt.wantLen)
			}
			if !date.Equal(tt.wantDate) {
				t.Errorf("date = %v, want %v", date, tt.wantDate)
			}
		})
	}

	t.Run("rate values are correct", func(t *testing.T) {
		xml := `<?xml version="1.0" encoding="UTF-8"?>
<gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref">
  <Cube>
    <Cube time="2026-03-24">
      <Cube currency="USD" rate="1.0834"/>
      <Cube currency="GBP" rate="0.8321"/>
    </Cube>
  </Cube>
</gesmes:Envelope>`
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(xml))
		}))
		defer server.Close()

		client := &ECBClient{httpClient: server.Client()}
		rates, _, err := client.fetchRates(context.Background(), server.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		rateMap := make(map[string]float64)
		for _, r := range rates {
			rateMap[r.Currency] = r.Rate
		}

		if rateMap["USD"] != 1.0834 {
			t.Errorf("USD rate = %f, want 1.0834", rateMap["USD"])
		}
		if rateMap["GBP"] != 0.8321 {
			t.Errorf("GBP rate = %f, want 0.8321", rateMap["GBP"])
		}
	})
}
