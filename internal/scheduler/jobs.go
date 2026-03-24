package scheduler

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/robfig/cron/v3"

	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/market"
)

// Scheduler manages periodic background jobs.
type Scheduler struct {
	cron       *cron.Cron
	queries    *db.Queries
	yahoo      *market.YahooClient
	ecb        *market.ECBClient
	backupPath string
}

func New(q *db.Queries, yahoo *market.YahooClient, ecb *market.ECBClient, backupPath string) *Scheduler {
	return &Scheduler{
		cron:       cron.New(cron.WithSeconds()),
		queries:    q,
		yahoo:      yahoo,
		ecb:        ecb,
		backupPath: backupPath,
	}
}

func (s *Scheduler) Start() error {
	// Weekdays 18:30: update prices
	if _, err := s.cron.AddFunc("0 30 18 * * 1-5", s.updatePrices); err != nil {
		return fmt.Errorf("add price update job: %w", err)
	}

	// Weekdays 16:00: update FX rates
	if _, err := s.cron.AddFunc("0 0 16 * * 1-5", s.updateFXRates); err != nil {
		return fmt.Errorf("add FX rate job: %w", err)
	}

	// Sundays 03:00: update ETF holdings
	if _, err := s.cron.AddFunc("0 0 3 * * 0", s.updateETFHoldings); err != nil {
		return fmt.Errorf("add ETF holdings job: %w", err)
	}

	// Daily 02:00: database backup
	if _, err := s.cron.AddFunc("0 0 2 * * *", s.backupDatabase); err != nil {
		return fmt.Errorf("add backup job: %w", err)
	}

	s.cron.Start()
	log.Println("scheduler started")
	return nil
}

func (s *Scheduler) Stop() {
	s.cron.Stop()
}

func (s *Scheduler) updatePrices() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	securities, err := s.queries.ListSecuritiesWithSymbol(ctx)
	if err != nil {
		log.Printf("ERROR: list securities for price update: %v", err)
		return
	}

	symbols := make(map[string]string)
	for _, sec := range securities {
		if sec.Symbol.Valid && sec.Symbol.String != "" {
			symbols[sec.ISIN] = sec.Symbol.String
		}
	}

	if len(symbols) == 0 {
		log.Println("no securities with symbols to update")
		return
	}

	results := s.yahoo.FetchPrices(ctx, symbols)
	updated := 0
	for _, r := range results {
		if r.Err != nil {
			log.Printf("WARNING: fetch price for %s: %v", r.Symbol, r.Err)
			continue
		}
		var price pgtype.Numeric
		price.Scan(fmt.Sprintf("%f", r.Price))
		if err := s.queries.UpsertMarketData(ctx, r.Symbol, r.Date, price, r.Currency); err != nil {
			log.Printf("WARNING: upsert market data for %s: %v", r.Symbol, err)
			continue
		}
		updated++
	}

	// Refresh holdings view
	if err := s.queries.RefreshCurrentHoldings(ctx); err != nil {
		log.Printf("WARNING: refresh holdings: %v", err)
	}

	log.Printf("price update: %d/%d securities updated", updated, len(symbols))
}

func (s *Scheduler) updateFXRates() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rates, date, err := s.ecb.FetchDailyRates(ctx)
	if err != nil {
		log.Printf("ERROR: fetch ECB rates: %v", err)
		return
	}

	updated := 0
	for _, r := range rates {
		var rate pgtype.Numeric
		rate.Scan(fmt.Sprintf("%f", r.Rate))
		if err := s.queries.UpsertExchangeRate(ctx, date, r.Currency, rate); err != nil {
			log.Printf("WARNING: upsert FX rate %s: %v", r.Currency, err)
			continue
		}
		updated++
	}

	log.Printf("FX rate update: %d rates for %s", updated, date.Format("2006-01-02"))
}

func (s *Scheduler) updateETFHoldings() {
	// Placeholder for ETF holdings refresh.
	// Will fetch from provider CSVs and justETF when implemented.
	log.Println("ETF holdings update: not yet implemented")
}

func (s *Scheduler) backupDatabase() {
	if s.backupPath == "" {
		log.Println("backup: no backup path configured")
		return
	}

	filename := fmt.Sprintf("%s/finance-backup-%s.sql.gz",
		s.backupPath,
		time.Now().Format("2006-01-02"),
	)

	cmd := exec.Command("sh", "-c",
		fmt.Sprintf("pg_dump $DATABASE_URL | gzip > %s", filename))

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("ERROR: backup: %v: %s", err, string(output))
		return
	}

	log.Printf("backup: saved to %s", filename)
}

// RunPriceUpdate triggers an immediate price update (for manual refresh).
func (s *Scheduler) RunPriceUpdate() {
	go s.updatePrices()
}

// RunFXUpdate triggers an immediate FX rate update.
func (s *Scheduler) RunFXUpdate() {
	go s.updateFXRates()
}
