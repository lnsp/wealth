package handler

import (
	"testing"
	"time"
)

func TestVestIsPast(t *testing.T) {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	now := time.Date(2026, 10, 6, 0, 30, 0, 0, berlin) // just after local midnight
	date := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

	cases := []struct {
		vest time.Time
		want bool
	}{
		{date(2026, 6, 25), true},
		{date(2026, 10, 5), true},
		{date(2026, 10, 6), false}, // vesting today is still upcoming
		{date(2026, 10, 25), false},
	}
	for _, c := range cases {
		if got := vestIsPast(c.vest, now); got != c.want {
			t.Errorf("vestIsPast(%s) = %v, want %v", c.vest.Format("2006-01-02"), got, c.want)
		}
	}
}
