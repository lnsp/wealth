package handler

import (
	"math"
	"testing"
	"time"
)

func TestBenchmarkReturnAt(t *testing.T) {
	prices := []benchmarkPrice{
		{date: time.Date(2022, 10, 1, 0, 0, 0, 0, time.UTC), price: 60.0},
		{date: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC), price: 63.0},
		{date: time.Date(2023, 7, 1, 0, 0, 0, 0, time.UTC), price: 66.0},
		{date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), price: 72.0},
		{date: time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC), price: 78.0},
		{date: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), price: 84.0},
	}

	t.Run("full period return", func(t *testing.T) {
		start := time.Date(2022, 10, 1, 0, 0, 0, 0, time.UTC)
		end := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		ret := benchmarkReturnAt(prices, start, end)
		if ret == nil {
			t.Fatal("expected non-nil return")
		}
		// (84-60)/60 * 100 = 40%
		if math.Abs(*ret-40.0) > 0.1 {
			t.Errorf("return = %f, want ~40.0%%", *ret)
		}
	})

	t.Run("same date returns zero", func(t *testing.T) {
		start := time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)
		ret := benchmarkReturnAt(prices, start, start)
		if ret == nil {
			t.Fatal("expected non-nil return")
		}
		if math.Abs(*ret) > 0.1 {
			t.Errorf("return = %f, want ~0%%", *ret)
		}
	})

	t.Run("mid-period return", func(t *testing.T) {
		start := time.Date(2022, 10, 1, 0, 0, 0, 0, time.UTC)
		mid := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		ret := benchmarkReturnAt(prices, start, mid)
		if ret == nil {
			t.Fatal("expected non-nil return")
		}
		// (72-60)/60 * 100 = 20%
		if math.Abs(*ret-20.0) > 0.1 {
			t.Errorf("return = %f, want ~20.0%%", *ret)
		}
	})

	t.Run("empty prices returns nil", func(t *testing.T) {
		ret := benchmarkReturnAt(nil, time.Now(), time.Now())
		if ret != nil {
			t.Error("expected nil for empty prices")
		}
	})
}

func TestFindClosestPrice(t *testing.T) {
	prices := []benchmarkPrice{
		{date: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), price: 70.0},
		{date: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC), price: 72.0},
		{date: time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC), price: 74.0},
	}

	t.Run("exact match", func(t *testing.T) {
		p := findClosestPrice(prices, time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC))
		if p != 72.0 {
			t.Errorf("price = %f, want 72.0", p)
		}
	})

	t.Run("closest to mid-month", func(t *testing.T) {
		p := findClosestPrice(prices, time.Date(2024, 2, 15, 0, 0, 0, 0, time.UTC))
		if p != 72.0 { // Feb 1 is 14 days away, Mar 1 is 15 days — Feb 1 is closer
			t.Errorf("price = %f, want 72.0", p)
		}
	})

	t.Run("too far away returns zero", func(t *testing.T) {
		p := findClosestPrice(prices, time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC))
		if p != 0 {
			t.Errorf("price = %f, want 0 (too far from any data point)", p)
		}
	})
}

func TestAbsDuration(t *testing.T) {
	if absDuration(-5*time.Second) != 5*time.Second {
		t.Error("negative duration not made positive")
	}
	if absDuration(5*time.Second) != 5*time.Second {
		t.Error("positive duration changed")
	}
}
