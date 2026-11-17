package scheduler

import (
	"context"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestFmtEUR_NonFiniteAndOverflow(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{-0.4, "0"},
		{62348.2, "62.348"},
		{-62348.7, "-62.349"},
		{math.NaN(), "0"},
		{math.Inf(1), "0"},
		{math.Inf(-1), "0"},
	}
	for _, tc := range cases {
		got := fmtEUR(tc.in)
		if got != tc.want {
			t.Errorf("fmtEUR(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// Extreme values >= 2^63 must not stack-overflow via int64 MinInt64 negation.
	_ = fmtEUR(1e20)
	_ = fmtEUR(-1e20)
}

func TestScheduler_JobStatusRaceAndSingleFlight(t *testing.T) {
	s := New(nil, nil, nil, nil, "", "")

	if !s.tryStartJob("prices") {
		t.Fatal("first tryStartJob should succeed")
	}
	if s.tryStartJob("prices") {
		t.Fatal("second concurrent tryStartJob must fail while running")
	}
	s.finishJob("prices")
	if !s.tryStartJob("prices") {
		t.Fatal("tryStartJob should succeed after finishJob")
	}
	s.finishJob("prices")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				s.setJobStatus("prices", "ok", "updated")
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = s.GetJobStatuses()
			}
		}()
	}
	wg.Wait()
}

func TestSSRF_SafeWebhookValidationAndDialer(t *testing.T) {
	blocked := []string{
		"http://127.0.0.1:8080/hook",
		"http://localhost:8080/hook",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.5/hook",
		"http://192.168.1.1/hook",
		"http://[::1]:8080/hook",
		"http://db.internal:5432/",
		"ftp://example.com/file",
	}
	for _, u := range blocked {
		if _, err := validateSafeWebhookURL(u); err == nil {
			t.Errorf("expected %q to be blocked by validateSafeWebhookURL", u)
		}
	}

	if !isDisallowedOutboundIP(net.ParseIP("100.100.100.100")) {
		t.Error("expected CGNAT 100.64.0.0/10 IP to be blocked")
	}

	// Verify custom dialer blocks loopback HTTP server even if URL validator were bypassed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := newSafeWebhookClient()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if _, err := client.Do(req); err == nil {
		t.Fatal("expected safe webhook client to block connection to loopback test server")
	}
}
