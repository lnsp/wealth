package scheduler

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/robfig/cron/v3"

	"github.com/lnsp/wealth/internal/analytics"
	"github.com/lnsp/wealth/internal/assets"
	db "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/market"
)

// fmtEUR formats a number with German thousands separator (e.g. 62348 -> "62.348")
func fmtEUR(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "0"
	}
	rounded := math.Round(v)
	if rounded < 0 {
		return "-" + fmtEUR(-rounded)
	}
	if rounded >= 1e15 {
		return fmt.Sprintf("%.0f", rounded)
	}
	n := int64(rounded)
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	result := make([]byte, 0, len(s)+len(s)/3)
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, '.')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

func isSecuritiesOnlyAccountType(accType string) bool {
	switch accType {
	case "brokerage", "crypto", "pension", "precious_metals":
		return true
	default:
		return false
	}
}

// JobStatus tracks the last run state of a scheduled job.
type JobStatus struct {
	Name     string     `json:"name"`
	Schedule string     `json:"schedule"`
	LastRun  *time.Time `json:"last_run"`
	Status   string     `json:"status"` // "ok", "error", "running", "never"
	Message  string     `json:"message,omitempty"`
}

// Scheduler manages periodic background jobs.
type Scheduler struct {
	cron         *cron.Cron
	queries      *db.Queries
	yahoo        *market.YahooClient
	ecb          *market.ECBClient
	justETF      *market.JustETFClient
	eurostat     *market.EurostatClient
	backupPath   string
	ageRecipient string

	mu         sync.Mutex
	jobs       map[string]*JobStatus
	running    map[string]bool
	snapshotMu sync.Mutex
}

func New(q *db.Queries, yahoo *market.YahooClient, ecb *market.ECBClient, justETF *market.JustETFClient, backupPath, ageRecipient string) *Scheduler {
	return &Scheduler{
		cron:         cron.New(cron.WithSeconds()),
		queries:      q,
		yahoo:        yahoo,
		ecb:          ecb,
		justETF:      justETF,
		eurostat:     market.NewEurostatClient(),
		backupPath:   backupPath,
		ageRecipient: ageRecipient,
		running:      make(map[string]bool),
		jobs: map[string]*JobStatus{
			"prices":       {Name: "Price Update", Schedule: "Weekdays 18:30", Status: "never"},
			"prices-live":  {Name: "Live Prices", Schedule: "Weekdays */15m (market hours)", Status: "never"},
			"fx":           {Name: "FX Rates", Schedule: "Weekdays 16:00", Status: "never"},
			"etf":          {Name: "ETF Metadata", Schedule: "Sundays 03:00", Status: "never"},
			"networth":     {Name: "Net Worth Snapshot", Schedule: "Daily 19:00", Status: "never"},
			"price-index":  {Name: "House Price Index", Schedule: "Mondays 06:30", Status: "never"},
			"backup":       {Name: "Database Backup", Schedule: "Daily 02:00", Status: "never"},
			"alerts":       {Name: "Price Alerts", Schedule: "Daily 19:05", Status: "never"},
			"report":       {Name: "Wealth Reports", Schedule: "1st of month 06:00", Status: "never"},
		},
	}
}

func (s *Scheduler) tryStartJob(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running[name] {
		return false
	}
	s.running[name] = true
	return true
}

func (s *Scheduler) finishJob(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.running, name)
}

// GetJobStatuses returns the current status of all scheduled jobs, sorted by name.
func (s *Scheduler) GetJobStatuses() []JobStatus {
	s.mu.Lock()
	result := make([]JobStatus, 0, len(s.jobs))
	for _, j := range s.jobs {
		cp := *j
		if j.LastRun != nil {
			lr := *j.LastRun
			cp.LastRun = &lr
		}
		result = append(result, cp)
	}
	s.mu.Unlock()
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func (s *Scheduler) setJobStatus(name, status, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jobs[name]; ok {
		now := time.Now()
		j.LastRun = &now
		j.Status = status
		// Sanitize error messages: strip URLs, IPs, DNS details
		if status == "error" {
			// Strip Go HTTP error format: Get "URL": error details
			if strings.HasPrefix(message, "Get \"") || strings.HasPrefix(message, "Post \"") {
				if idx := strings.Index(message, "\": "); idx > 0 {
					message = message[idx+3:] // keep only the error part
				}
			}
			// Further strip network internals
			if idx := strings.Index(message, ": dial"); idx > 0 {
				message = "network timeout"
			} else if idx := strings.Index(message, ": read"); idx > 0 {
				message = "connection error"
			}
		}
		j.Message = message
	}
}

func (s *Scheduler) Start() error {
	// Weekdays 18:30: update prices (end-of-day)
	if _, err := s.cron.AddFunc("0 30 18 * * 1-5", s.updatePrices); err != nil {
		return fmt.Errorf("add price update job: %w", err)
	}

	// Weekdays every 15 min during European market hours (08:00-17:30 CET): live intraday prices
	if _, err := s.cron.AddFunc("0 */15 * * * 1-5", s.updatePricesIntraday); err != nil {
		return fmt.Errorf("add intraday price job: %w", err)
	}

	// Weekdays 16:00: update FX rates
	if _, err := s.cron.AddFunc("0 0 16 * * 1-5", s.updateFXRates); err != nil {
		return fmt.Errorf("add FX rate job: %w", err)
	}

	// Sundays 03:00: update ETF holdings
	if _, err := s.cron.AddFunc("0 0 3 * * 0", s.updateETFHoldings); err != nil {
		return fmt.Errorf("add ETF holdings job: %w", err)
	}

	// Daily 19:00: compute net worth snapshot (after price update at 18:30)
	if _, err := s.cron.AddFunc("0 0 19 * * *", s.updateNetWorthSnapshot); err != nil {
		return fmt.Errorf("add net worth snapshot job: %w", err)
	}

	// Daily 19:05: evaluate price alerts (after net worth snapshot at 19:00)
	if _, err := s.cron.AddFunc("0 5 19 * * *", s.evaluateAlerts); err != nil {
		return fmt.Errorf("add alert job: %w", err)
	}
	// Weekly tax-loss harvesting check (Sundays at 10:00)
	if _, err := s.cron.AddFunc("0 0 10 * * 0", s.checkTaxLossHarvesting); err != nil {
		return fmt.Errorf("add alert evaluation job: %w", err)
	}

	// 1st of month 06:00: generate monthly wealth report + dispatch monthly/quarterly digests
	if _, err := s.cron.AddFunc("0 0 6 1 * *", s.generateMonthlyReport); err != nil {
		return fmt.Errorf("add monthly report job: %w", err)
	}

	// Mondays 07:00: dispatch weekly digest
	if _, err := s.cron.AddFunc("0 0 7 * * 1", s.sendWeeklyDigest); err != nil {
		return fmt.Errorf("add weekly digest job: %w", err)
	}

	// January 2nd 06:00: generate annual wealth report
	if _, err := s.cron.AddFunc("0 0 6 2 1 *", s.generateAnnualReport); err != nil {
		return fmt.Errorf("add annual report job: %w", err)
	}

	// Daily 02:00: database backup
	if _, err := s.cron.AddFunc("0 0 2 * * *", s.backupDatabase); err != nil {
		return fmt.Errorf("add backup job: %w", err)
	}

	// Daily 08:00: tax calendar reminders
	if _, err := s.cron.AddFunc("0 0 8 * * *", s.checkTaxCalendarReminders); err != nil {
		return fmt.Errorf("add tax calendar job: %w", err)
	}

	// Monday 06:30: house price index (Eurostat publishes quarterly)
	if _, err := s.cron.AddFunc("0 30 6 * * 1", s.updatePriceIndexes); err != nil {
		return fmt.Errorf("add price index job: %w", err)
	}

	// Monday 09:00: weekly data health digest
	if _, err := s.cron.AddFunc("0 0 9 * * 1", s.weeklyDataHealthDigest); err != nil {
		return fmt.Errorf("add data health job: %w", err)
	}

	s.cron.Start()
	log.Println("scheduler started")

	// Run initial data fetch in background if jobs have never run
	go s.runInitialFetch()

	return nil
}

// BackfillJobStatus represents the state of a one-time backfill job.
type BackfillJobStatus struct {
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
	Message     string     `json:"message,omitempty"`
	Attempts    int        `json:"attempts"`
}

// GetBackfillStatuses returns the status of all backfill jobs from the database.
func (s *Scheduler) GetBackfillStatuses(ctx context.Context) []BackfillJobStatus {
	rows, err := s.queries.DB().Query(ctx,
		`SELECT name, status, started_at, completed_at, message, attempts FROM backfill_jobs ORDER BY name`)
	if err != nil {
		log.Printf("WARNING: failed to query backfill jobs: %v", err)
		return nil
	}
	defer rows.Close()

	var result []BackfillJobStatus
	for rows.Next() {
		var b BackfillJobStatus
		if err := rows.Scan(&b.Name, &b.Status, &b.StartedAt, &b.CompletedAt, &b.Message, &b.Attempts); err != nil {
			log.Printf("WARNING: failed to scan backfill job: %v", err)
			continue
		}
		result = append(result, b)
	}
	if err := rows.Err(); err != nil {
		log.Printf("WARNING: iterate backfill jobs: %v", err)
	}
	return result
}

// backfillNeeded checks if a backfill job needs to run (not yet completed).
func (s *Scheduler) backfillNeeded(ctx context.Context, name string) bool {
	var status string
	err := s.queries.DB().QueryRow(ctx,
		`SELECT status FROM backfill_jobs WHERE name = $1`, name,
	).Scan(&status)
	if err != nil {
		return true // not found = needs to run
	}
	return status != "completed"
}

// backfillStart marks a backfill job as running.
func (s *Scheduler) backfillStart(ctx context.Context, name string) {
	s.queries.DB().Exec(ctx,
		`INSERT INTO backfill_jobs (name, status, started_at, attempts)
		 VALUES ($1, 'running', NOW(), 1)
		 ON CONFLICT (name) DO UPDATE SET status = 'running', started_at = NOW(), attempts = backfill_jobs.attempts + 1`,
		name,
	)
}

// backfillComplete marks a backfill job as completed.
func (s *Scheduler) backfillComplete(ctx context.Context, name, message string) {
	s.queries.DB().Exec(ctx,
		`UPDATE backfill_jobs SET status = 'completed', completed_at = NOW(), message = $2 WHERE name = $1`,
		name, message,
	)
}

// backfillFail marks a backfill job as failed.
func (s *Scheduler) backfillFail(ctx context.Context, name, message string) {
	s.queries.DB().Exec(ctx,
		`UPDATE backfill_jobs SET status = 'failed', message = $2 WHERE name = $1`,
		name, message,
	)
}

// runInitialFetch runs data jobs on startup, skipping already-completed backfills.
func (s *Scheduler) runInitialFetch() {
	// Small delay to let the server fully start
	time.Sleep(5 * time.Second)

	ctx := context.Background()

	// Always run current data fetches (cheap, ensure freshness)
	log.Println("initial fetch: running ETF metadata update")
	s.updateETFHoldings()
	log.Println("initial fetch: running price update")
	s.updatePrices()
	log.Println("initial fetch: running FX rate update")
	s.updateFXRates()
	log.Println("initial fetch: running house price index update")
	s.updatePriceIndexes()

	// Backfills: only run if not already completed
	if s.backfillNeeded(ctx, "historical_prices") {
		log.Println("initial fetch: backfilling historical prices")
		s.backfillStart(ctx, "historical_prices")
		if err := s.backfillHistoricalPrices(); err != nil {
			s.backfillFail(ctx, "historical_prices", err.Error())
		} else {
			s.backfillComplete(ctx, "historical_prices", "done")
		}
	} else {
		log.Println("initial fetch: historical prices already backfilled, skipping")
	}

	if s.backfillNeeded(ctx, "historical_fx") {
		log.Println("initial fetch: backfilling historical FX rates")
		s.backfillStart(ctx, "historical_fx")
		if err := s.backfillHistoricalFXRates(); err != nil {
			s.backfillFail(ctx, "historical_fx", err.Error())
		} else {
			s.backfillComplete(ctx, "historical_fx", "done")
		}
	} else {
		log.Println("initial fetch: historical FX rates already backfilled, skipping")
	}

	if s.backfillNeeded(ctx, "historical_networth") {
		log.Println("initial fetch: backfilling historical net worth snapshots")
		s.backfillStart(ctx, "historical_networth")
		if err := s.backfillNetWorthSnapshots(); err != nil {
			s.backfillFail(ctx, "historical_networth", err.Error())
		} else {
			s.backfillComplete(ctx, "historical_networth", "done")
		}
	} else {
		log.Println("initial fetch: historical net worth already backfilled, skipping")
	}

	// Always backfill missing reports on startup
	log.Println("initial fetch: backfilling missing reports")
	s.backfillReports()
}

func (s *Scheduler) Stop() {
	s.cron.Stop()
}

// notifyAndDispatch inserts a notification and dispatches it to configured alert channels.
func (s *Scheduler) notifyAndDispatch(ctx context.Context, id uuid.UUID, message string, val pgtype.Numeric) {
	alertID := &id
	if id == uuid.Nil {
		alertID = nil
	}
	if err := s.queries.InsertNotification(ctx, db.InsertNotificationParams{
		AlertID: alertID, Message: message, Value: val,
	}); err != nil {
		log.Printf("WARNING: insert notification failed: %v", err)
	}
	// Extract a short subject from the message (first bracket tag or first 50 chars)
	subject := message
	if idx := strings.Index(message, "] "); idx > 0 {
		subject = message[1:idx] // e.g. "[Contribution Review]" -> "Contribution Review"
	} else if len(subject) > 50 {
		subject = subject[:50]
	}
	s.dispatchMessage(ctx, "alerts", subject, message)
}

// sendWeeklyDigest dispatches a brief weekly summary to channels with digest_frequency="weekly".
func (s *Scheduler) sendWeeklyDigest() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Get current net worth from latest snapshot
	snapshots, err := s.queries.ListNetWorthSnapshots(ctx, 7)
	if err != nil || len(snapshots) == 0 {
		return
	}
	latest := snapshots[0]
	var nw float64
	if latest.Total.Valid {
		f, _ := latest.Total.Float64Value()
		nw = f.Float64
	}

	weekStart := ""
	if len(snapshots) > 1 {
		var prev float64
		last := snapshots[len(snapshots)-1]
		if last.Total.Valid {
			f, _ := last.Total.Float64Value()
			prev = f.Float64
		}
		change := nw - prev
		changePct := 0.0
		if prev > 0 {
			changePct = change / prev * 100
		}
		sign := "+"
		if change < 0 {
			sign = ""
		}
		weekStart = fmt.Sprintf("\nWeekly Change: %s%s EUR (%.1f%%)", sign, fmtEUR(change), changePct)
	}

	subject := fmt.Sprintf("Wealth Weekly — %s", time.Now().Format("02 Jan 2006"))
	body := fmt.Sprintf("Net Worth: %s EUR%s", fmtEUR(nw), weekStart)

	s.dispatchMessage(ctx, "digest", subject, body, "weekly")
	log.Printf("weekly digest dispatched")
}

// isEuropeanMarketHours returns true during Mon-Fri 08:00-22:00 CET,
// covering all German exchanges (Xetra, Frankfurt, Tradegate, gettex, Stuttgart).
func isEuropeanMarketHours() bool {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		loc = time.UTC // fallback
	}
	now := time.Now().In(loc)
	wd := now.Weekday()
	if wd == time.Saturday || wd == time.Sunday {
		return false
	}
	hour := now.Hour()
	return hour >= 8 && hour < 22
}

// updatePricesIntraday runs updatePrices only during European market hours.
func (s *Scheduler) updatePricesIntraday() {
	if !isEuropeanMarketHours() {
		return
	}
	if !s.tryStartJob("prices-live") {
		return
	}
	defer s.finishJob("prices-live")

	s.setJobStatus("prices-live", "running", "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	securities, err := s.queries.ListSecuritiesWithSymbol(ctx)
	if err != nil {
		log.Printf("ERROR: intraday price update: %v", err)
		s.setJobStatus("prices-live", "error", err.Error())
		return
	}

	symbols := make(map[string]string)
	for _, sec := range securities {
		if sec.Symbol.Valid && sec.Symbol.String != "" {
			symbols[sec.ISIN] = sec.Symbol.String
		}
	}
	if len(symbols) == 0 {
		s.setJobStatus("prices-live", "ok", "no securities with symbols")
		return
	}

	results := s.yahoo.FetchPrices(ctx, symbols)
	updated := 0
	for _, r := range results {
		if r.Err != nil {
			continue
		}
		var price pgtype.Numeric
		price.Scan(fmt.Sprintf("%f", r.Price))
		if err := s.queries.UpsertMarketData(ctx, db.UpsertMarketDataParams{
			SecurityISIN: r.Symbol, Date: r.Date, Close: price, Currency: r.Currency,
		}); err != nil {
			continue
		}
		updated++
	}
	// Refresh holdings view
	if err := s.queries.RefreshCurrentHoldings(ctx); err != nil {
		log.Printf("WARNING: intraday refresh holdings: %v", err)
	}

	s.setJobStatus("prices-live", "ok", fmt.Sprintf("updated %d/%d prices", updated, len(symbols)))
	log.Printf("intraday price update: %d/%d prices updated", updated, len(symbols))

	// Update today's net worth snapshot with fresh prices
	s.updateNetWorthSnapshot()
}

func (s *Scheduler) updatePrices() {
	if !s.tryStartJob("prices") {
		return
	}
	defer s.finishJob("prices")

	s.setJobStatus("prices", "running", "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	securities, err := s.queries.ListSecuritiesWithSymbol(ctx)
	if err != nil {
		log.Printf("ERROR: list securities for price update: %v", err)
		s.setJobStatus("prices", "error", err.Error())
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
		s.setJobStatus("prices", "ok", "no securities with symbols")
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
		if err := s.queries.UpsertMarketData(ctx, db.UpsertMarketDataParams{
			SecurityISIN: r.Symbol, Date: r.Date, Close: price, Currency: r.Currency,
		}); err != nil {
			log.Printf("WARNING: upsert market data for %s: %v", r.Symbol, err)
			continue
		}
		updated++
	}

	// Refresh holdings view
	if err := s.queries.RefreshCurrentHoldings(ctx); err != nil {
		log.Printf("WARNING: refresh holdings: %v", err)
	}

	msg := fmt.Sprintf("%d/%d securities updated", updated, len(symbols))
	log.Printf("price update: %s", msg)
	s.setJobStatus("prices", "ok", msg)

	// Update today's net worth snapshot with fresh prices
	s.updateNetWorthSnapshot()
}

func (s *Scheduler) updateFXRates() {
	if !s.tryStartJob("fx") {
		return
	}
	defer s.finishJob("fx")

	s.setJobStatus("fx", "running", "")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Retry up to 3 times with backoff — ECB endpoint can be intermittently slow
	var rates []market.FXRate
	var date time.Time
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		rates, date, err = s.ecb.FetchDailyRates(ctx)
		if err == nil {
			break
		}
		if attempt < 2 {
			log.Printf("WARNING: ECB fetch attempt %d failed: %v, retrying...", attempt+1, err)
			time.Sleep(time.Duration(attempt+1) * 5 * time.Second)
		}
	}
	if err != nil {
		s.setJobStatus("fx", "error", err.Error())
		log.Printf("ERROR: fetch ECB rates after 3 attempts: %v", err)
		return
	}

	updated := 0
	for _, r := range rates {
		var rate pgtype.Numeric
		rate.Scan(fmt.Sprintf("%f", r.Rate))
		if err := s.queries.UpsertExchangeRate(ctx, db.UpsertExchangeRateParams{
			Date: date, Currency: r.Currency, Rate: rate,
		}); err != nil {
			log.Printf("WARNING: upsert FX rate %s: %v", r.Currency, err)
			continue
		}
		updated++
	}

	msg := fmt.Sprintf("%d rates for %s", updated, date.Format("2006-01-02"))
	s.setJobStatus("fx", "ok", msg)
	log.Printf("FX rate update: %s", msg)
}

// fundDataTarget decides what the fund-metadata job does for an asset class:
// fetch justETF data for ETFs, bond funds and other funds, and probe single
// "stocks" in case the name heuristic misclassified a fund.
func fundDataTarget(assetClass string) (fetch, probe bool) {
	switch assetClass {
	case "etf", "bond", "fund":
		return true, false
	case "stock":
		return false, true
	}
	return false, false
}

// metadataProvesEquityFund reports whether justETF data is strong enough to
// reclassify a "stock" as an ETF: an ongoing charge and a sector breakdown.
// ETCs and bond funds lack the sector breakdown and are left alone, since
// reclassifying them as equity ETFs would grant a 30 % Teilfreistellung.
func metadataProvesEquityFund(meta *market.ETFMetadata) bool {
	return meta != nil && meta.TER > 0 && len(meta.SectorWeights) > 0
}

func (s *Scheduler) updateETFHoldings() {
	if !s.tryStartJob("etf") {
		return
	}
	defer s.finishJob("etf")

	s.setJobStatus("etf", "running", "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Get all ETFs with holdings
	holdings, err := s.queries.ListCurrentHoldings(ctx)
	if err != nil {
		log.Printf("ERROR: ETF holdings update: list holdings: %v", err)
		s.setJobStatus("etf", "error", err.Error())
		return
	}

	secs, _ := s.queries.ListSecurities(ctx)
	secByISIN := make(map[string]db.Security, len(secs))
	for _, sec := range secs {
		secByISIN[sec.ISIN] = sec
	}

	seenISIN := make(map[string]bool)
	updated := 0
	reclassified := 0
	for _, h := range holdings {
		fetch, probe := fundDataTarget(h.AssetClass)
		if (!fetch && !probe) || seenISIN[h.SecurityISIN] {
			continue
		}
		seenISIN[h.SecurityISIN] = true

		meta, err := s.justETF.FetchMetadata(ctx, h.SecurityISIN)
		if err != nil {
			if fetch {
				log.Printf("WARNING: ETF holdings update: fetch %s: %v", h.SecurityISIN, err)
			}
			continue
		}
		if probe {
			// A "stock" that justETF profiles with a charge and a sector
			// breakdown is an equity fund the name heuristic missed (e.g.
			// "ISHSIII-MSCI EM USD(ACC)"). Anything less is left alone.
			if !metadataProvesEquityFund(meta) {
				continue
			}
			if err := s.queries.UpdateSecurityAssetClass(ctx, db.UpdateSecurityAssetClassParams{
				ISIN: h.SecurityISIN, AssetClass: "etf", AssetClass_2: h.AssetClass,
			}); err != nil {
				log.Printf("WARNING: ETF holdings update: reclassify %s: %v", h.SecurityISIN, err)
				continue
			}
			reclassified++
			log.Printf("ETF metadata: reclassified %s from %s to etf (justETF fund profile)", h.SecurityISIN, h.AssetClass)
		}

		existingSec := secByISIN[h.SecurityISIN]

		// Preserve previously stored non-empty metadata fields when a partial scrape returns empty
		var sectorJSON []byte
		if len(meta.SectorWeights) > 0 {
			sectorJSON, _ = json.Marshal(meta.SectorWeights)
		} else if len(existingSec.SectorWeights) > 2 {
			sectorJSON = existingSec.SectorWeights
		} else {
			sectorJSON = []byte("{}")
		}

		var countryJSON []byte
		if len(meta.CountryWeights) > 0 {
			countryJSON, _ = json.Marshal(meta.CountryWeights)
		} else if len(existingSec.CountryWeights) > 2 {
			countryJSON = existingSec.CountryWeights
		} else {
			countryJSON = []byte("{}")
		}

		var ter pgtype.Numeric
		if meta.TER > 0 {
			ter.Scan(fmt.Sprintf("%.4f", meta.TER))
		} else if existingSec.TER.Valid {
			ter = existingSec.TER
		}

		if err := s.queries.UpdateSecurityMetadata(ctx, db.UpdateSecurityMetadataParams{
			ISIN: h.SecurityISIN, SectorWeights: sectorJSON, CountryWeights: countryJSON, TER: ter,
		}); err != nil {
			log.Printf("WARNING: ETF holdings update: store metadata for %s: %v", h.SecurityISIN, err)
			continue
		}

		// Also fetch individual holdings (served from JustETFClient's HTML cache without a 2nd HTTP GET)
		holdings2, err := s.justETF.FetchHoldings(ctx, h.SecurityISIN)
		if err != nil {
			log.Printf("WARNING: ETF holdings fetch %s: %v", h.SecurityISIN, err)
		} else if len(holdings2) > 0 {
			if err := s.queries.DeleteETFHoldingsByETF(ctx, h.SecurityISIN); err != nil {
				log.Printf("WARNING: clear stale ETF holdings %s: %v", h.SecurityISIN, err)
			}
			today := time.Now().UTC().Truncate(24 * time.Hour)
			for _, hld := range holdings2 {
				var wp pgtype.Numeric
				wp.Scan(fmt.Sprintf("%.4f", hld.Weight))
				var sector, country pgtype.Text
				if hld.Sector != "" {
					sector = pgtype.Text{String: hld.Sector, Valid: true}
				}
				if hld.Country != "" {
					country = pgtype.Text{String: hld.Country, Valid: true}
				}
				if err := s.queries.UpsertETFHolding(ctx, db.UpsertETFHoldingParams{
					ETFISIN: h.SecurityISIN, HoldingISIN: hld.ISIN, HoldingName: hld.Name,
					WeightPct: wp, Sector: sector, Country: country, AsOfDate: today,
				}); err != nil {
					log.Printf("WARNING: store holding %s/%s: %v", h.SecurityISIN, hld.ISIN, err)
				}
			}
			log.Printf("ETF holdings stored: %s (%d positions)", h.SecurityISIN, len(holdings2))
		}

		updated++
		log.Printf("ETF metadata updated: %s (sectors=%d, countries=%d, TER=%.2f%%)",
			h.SecurityISIN, len(meta.SectorWeights), len(meta.CountryWeights), meta.TER)
	}

	msg := fmt.Sprintf("%d funds updated", updated)
	if reclassified > 0 {
		msg += fmt.Sprintf(", %d reclassified as ETF", reclassified)
	}
	log.Printf("ETF holdings update: %s", msg)
	s.setJobStatus("etf", "ok", msg)
}

func (s *Scheduler) backupDatabase() {
	if !s.tryStartJob("backup") {
		return
	}
	defer s.finishJob("backup")

	if s.backupPath == "" {
		log.Println("backup: no backup path configured")
		s.setJobStatus("backup", "error", "no backup path configured")
		return
	}
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Println("backup: DATABASE_URL not set")
		s.setJobStatus("backup", "error", "DATABASE_URL not set")
		return
	}

	s.setJobStatus("backup", "running", "")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	now := time.Now().UTC()
	dateStr := now.Format("2006-01-02")
	encrypted := s.ageRecipient != ""

	if encrypted && (strings.HasPrefix(s.ageRecipient, "-") || strings.ContainsAny(s.ageRecipient, " \t\r\n")) {
		log.Println("ERROR: backup failed: invalid BACKUP_AGE_RECIPIENT")
		s.setJobStatus("backup", "error", "invalid BACKUP_AGE_RECIPIENT")
		return
	}
	if !encrypted {
		log.Println("SECURITY WARNING: backup encryption not configured (BACKUP_AGE_RECIPIENT not set). Backups contain unencrypted database dumps.")
	}

	var filename string
	if encrypted {
		filename = fmt.Sprintf("%s/finance-backup-%s.sql.gz.age", s.backupPath, dateStr)
	} else {
		filename = fmt.Sprintf("%s/finance-backup-%s.sql.gz", s.backupPath, dateStr)
	}
	tmpFile := filename + ".tmp"
	_ = os.Remove(tmpFile)

	sanitizeErr := func(raw string) string {
		return strings.TrimSpace(strings.ReplaceAll(raw, dbURL, "[DATABASE_URL]"))
	}
	failBackup := func(reason string) {
		_ = os.Remove(tmpFile)
		clean := sanitizeErr(reason)
		log.Printf("ERROR: backup failed: %s", clean)
		s.setJobStatus("backup", "error", "backup failed: "+clean)
	}

	pgDump := exec.CommandContext(ctx, "pg_dump", "--dbname="+dbURL)
	pgStdout, err := pgDump.StdoutPipe()
	if err != nil {
		failBackup(err.Error())
		return
	}
	var pgStderr bytes.Buffer
	pgDump.Stderr = &pgStderr
	if err := pgDump.Start(); err != nil {
		failBackup(err.Error())
		return
	}

	var rawBytes int64
	if encrypted {
		ageCmd := exec.CommandContext(ctx, "age", "-r", s.ageRecipient, "-o", tmpFile)
		ageStdin, err := ageCmd.StdinPipe()
		if err != nil {
			_ = pgDump.Process.Kill()
			_ = pgDump.Wait()
			failBackup(err.Error())
			return
		}
		var ageStderr bytes.Buffer
		ageCmd.Stderr = &ageStderr
		if err := ageCmd.Start(); err != nil {
			_ = pgDump.Process.Kill()
			_ = pgDump.Wait()
			failBackup("age start: " + err.Error())
			return
		}

		gz := gzip.NewWriter(ageStdin)
		rawBytes, err = io.Copy(gz, pgStdout)
		if err != nil {
			_ = pgStdout.Close()
			if pgDump.Process != nil {
				_ = pgDump.Process.Kill()
			}
		}
		gzErr := gz.Close()
		stdinErr := ageStdin.Close()
		pgErr := pgDump.Wait()
		ageErr := ageCmd.Wait()

		switch {
		case pgErr != nil:
			failBackup("pg_dump: " + pgStderr.String() + " " + pgErr.Error())
			return
		case err != nil:
			failBackup("stream dump: " + err.Error())
			return
		case gzErr != nil:
			failBackup("gzip: " + gzErr.Error())
			return
		case stdinErr != nil:
			failBackup("age stdin: " + stdinErr.Error())
			return
		case ageErr != nil:
			failBackup("age: " + ageStderr.String() + " " + ageErr.Error())
			return
		}
	} else {
		outFile, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			_ = pgDump.Process.Kill()
			_ = pgDump.Wait()
			failBackup(err.Error())
			return
		}
		gz := gzip.NewWriter(outFile)
		rawBytes, err = io.Copy(gz, pgStdout)
		if err != nil {
			_ = pgStdout.Close()
			if pgDump.Process != nil {
				_ = pgDump.Process.Kill()
			}
		}
		gzErr := gz.Close()
		syncErr := outFile.Sync()
		closeErr := outFile.Close()
		pgErr := pgDump.Wait()

		switch {
		case pgErr != nil:
			failBackup("pg_dump: " + pgStderr.String() + " " + pgErr.Error())
			return
		case err != nil:
			failBackup("stream dump: " + err.Error())
			return
		case gzErr != nil:
			failBackup("gzip: " + gzErr.Error())
			return
		case syncErr != nil:
			failBackup("sync: " + syncErr.Error())
			return
		case closeErr != nil:
			failBackup("close: " + closeErr.Error())
			return
		}
	}

	stat, statErr := os.Stat(tmpFile)
	if statErr != nil || stat.Size() == 0 || rawBytes == 0 {
		failBackup("empty backup output")
		return
	}
	if err := os.Rename(tmpFile, filename); err != nil {
		failBackup("rename: " + err.Error())
		return
	}

	suffix := ""
	if encrypted {
		suffix = " (age encrypted)"
	}
	msg := fmt.Sprintf("saved to %s%s", filename, suffix)
	log.Printf("backup: %s", msg)
	s.setJobStatus("backup", "ok", msg)

	// Retention policy: keep 30 daily backups + 12 monthly (1st of month)
	s.enforceBackupRetention()
}

func (s *Scheduler) enforceBackupRetention() {
	entries, err := os.ReadDir(s.backupPath)
	if err != nil {
		log.Printf("backup retention: cannot read directory: %v", err)
		return
	}

	now := time.Now()
	dailyCutoff := now.AddDate(0, 0, -30)   // keep 30 days of daily backups
	monthlyCutoff := now.AddDate(-1, 0, 0)   // keep 12 months of monthly backups

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "finance-backup-") {
			continue
		}

		// Parse date from filename: finance-backup-YYYY-MM-DD.sql.gz[.age]
		if len(name) < 30 {
			continue
		}
		dateStr := name[15:25] // extract YYYY-MM-DD
		fileDate, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}

		// Keep if within daily retention window
		if fileDate.After(dailyCutoff) {
			continue
		}

		// Keep monthly backups (1st of month) within monthly window
		if fileDate.Day() == 1 && fileDate.After(monthlyCutoff) {
			continue
		}

		// Delete old backup
		path := fmt.Sprintf("%s/%s", s.backupPath, name)
		if err := os.Remove(path); err != nil {
			log.Printf("backup retention: cannot remove %s: %v", name, err)
		} else {
			log.Printf("backup retention: removed %s (older than retention policy)", name)
		}
	}
}

// RunPriceUpdate triggers an immediate price update (for manual refresh).
func (s *Scheduler) RunPriceUpdate() {
	go s.updatePrices()
}

// RunFXUpdate triggers an immediate FX rate update.
func (s *Scheduler) RunFXUpdate() {
	go s.updateFXRates()
}

// RunETFUpdate triggers an immediate ETF metadata update.
func (s *Scheduler) RunETFUpdate() {
	go s.updateETFHoldings()
}

// RunRebuildNetWorth triggers an immediate net worth snapshot backfill with single-flight protection.
func (s *Scheduler) RunRebuildNetWorth() {
	if !s.tryStartJob("backfill:networth") {
		return
	}
	go func() {
		defer s.finishJob("backfill:networth")
		s.BackfillNetWorthSnapshots()
		s.updateNetWorthSnapshot()
	}()
}

// updatePriceIndexes refreshes the published house price indices that
// properties can follow. A new or revised quarter changes past property
// values, so net-worth history is rebuilt when an asset follows the index.
func (s *Scheduler) updatePriceIndexes() {
	if !s.tryStartJob("price-index") {
		return
	}
	defer s.finishJob("price-index")

	s.setJobStatus("price-index", "running", "")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	changed, latest := 0, ""
	for _, series := range assets.IndexSeriesList {
		points, err := s.eurostat.FetchHousePriceIndex(ctx, series.Geo, series.Purchase)
		if err != nil {
			log.Printf("ERROR: price index %s: %v", series.ID, err)
			s.setJobStatus("price-index", "error", err.Error())
			return
		}
		for _, p := range points {
			var v pgtype.Numeric
			v.Scan(fmt.Sprintf("%.4f", p.Value))
			_, err := s.queries.UpsertPriceIndexPoint(ctx, db.UpsertPriceIndexPointParams{Series: series.ID, Period: p.Period, Value: v})
			switch {
			case err == nil:
				changed++
			case !errors.Is(err, pgx.ErrNoRows): // ErrNoRows = unchanged
				log.Printf("WARNING: price index %s %s: %v", series.ID, p.Period.Format("2006-01"), err)
			}
		}
		if len(points) > 0 {
			last := points[len(points)-1]
			latest = fmt.Sprintf("%d-Q%d = %.1f", last.Period.Year(), (int(last.Period.Month())+2)/3, last.Value)
		}
	}
	msg := fmt.Sprintf("%d new or revised quarters, latest %s", changed, latest)
	log.Printf("price index update: %s", msg)
	s.setJobStatus("price-index", "ok", msg)

	if changed > 0 && s.anyAssetFollowsIndex(ctx) {
		go func() {
			s.BackfillNetWorthSnapshots()
			s.updateNetWorthSnapshot()
		}()
	}
}

func (s *Scheduler) anyAssetFollowsIndex(ctx context.Context) bool {
	rows, err := s.queries.ListAssets(ctx)
	if err != nil {
		return false
	}
	for _, a := range rows {
		if a.PriceIndex != "" {
			return true
		}
	}
	return false
}

// RunNetWorthSnapshot triggers an immediate net worth snapshot.
func (s *Scheduler) RunNetWorthSnapshot() {
	go s.updateNetWorthSnapshot()
}

// RunAlertCheck triggers an immediate price-alert evaluation. Used by the
// manual-trigger endpoint so users (and QA) can verify alerts fire without
// waiting for the daily 19:05 schedule.
func (s *Scheduler) RunAlertCheck() {
	go s.evaluateAlerts()
}

// RunHistoricalPricesBackfill resets and re-runs the historical prices backfill.
func (s *Scheduler) RunHistoricalPricesBackfill() {
	if !s.tryStartJob("backfill:historical_prices") {
		return
	}
	go func() {
		defer s.finishJob("backfill:historical_prices")
		ctx := context.Background()
		s.backfillStart(ctx, "historical_prices")
		if err := s.backfillHistoricalPrices(); err != nil {
			s.backfillFail(ctx, "historical_prices", err.Error())
		} else {
			s.backfillComplete(ctx, "historical_prices", "done (manual)")
		}
	}()
}

func (s *Scheduler) updateNetWorthSnapshot() {
	s.snapshotMu.Lock()
	defer s.snapshotMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Sum cash balance across all active accounts, converting multi-currency balances
	// to EUR and clamping synthetic negative cash on securities-only accounts.
	accounts, err := s.queries.ListAccounts(ctx)
	if err != nil {
		log.Printf("ERROR: net worth snapshot: list accounts: %v", err)
		return
	}
	accTypes := make(map[uuid.UUID]string, len(accounts))
	for _, acc := range accounts {
		accTypes[acc.ID] = acc.Type
	}

	cashRows, err := s.queries.ListAllAccountCashByCurrency(ctx)
	var totalCash float64
	if err == nil {
		accCashEUR := make(map[uuid.UUID]float64, len(accounts))
		accExplicit := make(map[uuid.UUID]bool, len(accounts))
		for _, row := range cashRows {
			accCashEUR[row.AccountID] += convertToEURScheduler(ctx, s.queries, numericToFloat(row.Balance), row.Currency)
			if row.HasExplicitCash {
				accExplicit[row.AccountID] = true
			}
		}
		for _, acc := range accounts {
			bal := accCashEUR[acc.ID]
			if bal < 0 && !accExplicit[acc.ID] && isSecuritiesOnlyAccountType(accTypes[acc.ID]) {
				bal = 0
			}
			totalCash += bal
		}
	} else {
		for _, acc := range accounts {
			balance, err := s.queries.GetCashBalance(ctx, acc.ID)
			if err != nil {
				log.Printf("WARNING: net worth snapshot: cash balance for %s: %v", acc.Name, err)
				continue
			}
			totalCash += convertToEURScheduler(ctx, s.queries, numericToFloat(balance), acc.Currency)
		}
	}

	// Sum investment value: holdings × latest market price
	holdings, err := s.queries.ListCurrentHoldings(ctx)
	if err != nil {
		log.Printf("ERROR: net worth snapshot: list holdings: %v", err)
		return
	}

	var totalInvestment float64
	for _, h := range holdings {
		qty := numericToFloat(h.Quantity)
		if qty <= 0 {
			continue
		}

		// Try to get the latest market price
		priceRow, err := s.queries.GetLatestPrice(ctx, h.SecurityISIN)
		if err != nil {
			// No market price available — fall back to cost basis
			totalInvestment += convertToEURScheduler(ctx, s.queries, qty*numericToFloat(h.AvgCostBasis), h.Currency)
			continue
		}
		totalInvestment += convertToEURScheduler(ctx, s.queries, qty*numericToFloat(priceRow.Close), priceRow.Currency)
	}

	today := time.Now().UTC()
	date := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)

	// Physical assets at their modelled value, minus the loans on them
	var totalPhysical, totalDebt float64
	if holdings, err := assets.Load(ctx, s.queries); err != nil {
		log.Printf("WARNING: net worth snapshot: load assets: %v", err)
	} else {
		totalPhysical = holdings.PhysicalAt(date)
		totalDebt = holdings.DebtAt(date)
	}

	total := totalCash + totalInvestment + totalPhysical - totalDebt

	var totalNum, cashNum, investNum, physicalNum, debtNum pgtype.Numeric
	totalNum.Scan(fmt.Sprintf("%.4f", total))
	cashNum.Scan(fmt.Sprintf("%.4f", totalCash))
	investNum.Scan(fmt.Sprintf("%.4f", totalInvestment))
	physicalNum.Scan(fmt.Sprintf("%.4f", totalPhysical))
	debtNum.Scan(fmt.Sprintf("%.4f", totalDebt))

	if err := s.queries.UpsertNetWorthSnapshot(ctx, db.UpsertNetWorthSnapshotParams{
		Date: date, Total: totalNum, CashComponent: cashNum, InvestmentComponent: investNum,
		PhysicalComponent: physicalNum, DebtComponent: debtNum,
	}); err != nil {
		log.Printf("ERROR: net worth snapshot: upsert: %v", err)
		return
	}

	// Also record an intraday data point (for 1D chart)
	if err := s.queries.InsertNetWorthIntraday(ctx, db.InsertNetWorthIntradayParams{
		RecordedAt: today, Total: totalNum, CashComponent: cashNum, InvestmentComponent: investNum,
		PhysicalComponent: physicalNum, DebtComponent: debtNum,
	}); err != nil {
		log.Printf("WARNING: net worth intraday insert: %v", err)
	}

	// Prune old intraday data
	if err := s.queries.PruneNetWorthIntraday(ctx); err != nil {
		log.Printf("WARNING: prune intraday data: %v", err)
	}

	msg := fmt.Sprintf("total=%.0f (cash=%.0f + invest=%.0f + physical=%.0f - debt=%.0f)", total, totalCash, totalInvestment, totalPhysical, totalDebt)
	log.Printf("net worth snapshot: %s", msg)
	s.setJobStatus("networth", "ok", msg)
}

// BackfillNetWorthSnapshots replays the transaction log to compute daily
// historical net worth snapshots. Preloads all price history into memory
// and iterates day-by-day from the first transaction to today.
// backfillHistoricalPrices fetches 5 years of monthly historical prices
// for all securities with ticker symbols and stores them in market_data.
// Failed securities are retried up to 3 times with exponential backoff.
func (s *Scheduler) backfillHistoricalPrices() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	securities, err := s.queries.ListSecuritiesWithSymbol(ctx)
	if err != nil {
		log.Printf("ERROR: backfill prices: list securities: %v", err)
		return err
	}

	type secEntry struct {
		isin   string
		symbol string
	}
	var pending []secEntry
	for _, sec := range securities {
		if sec.Symbol.Valid && sec.Symbol.String != "" {
			pending = append(pending, secEntry{isin: sec.ISIN, symbol: sec.Symbol.String})
		}
	}
	totalSymbols := len(pending)

	totalStored := 0
	const maxRetries = 3
	for attempt := 0; attempt < maxRetries && len(pending) > 0; attempt++ {
		if attempt > 0 {
			delay := time.Duration(attempt*attempt) * 5 * time.Second
			log.Printf("backfill prices: retrying %d failed securities (attempt %d/%d, waiting %s)", len(pending), attempt+1, maxRetries, delay)
			time.Sleep(delay)
		}

		var failed []secEntry
		for _, sec := range pending {
			prices, err := s.yahoo.FetchHistoricalPrices(ctx, sec.symbol)
			if err != nil {
				log.Printf("WARNING: backfill prices for %s (%s): %v", sec.isin, sec.symbol, err)
				failed = append(failed, sec)
				continue
			}
			for _, p := range prices {
				var close pgtype.Numeric
				close.Scan(fmt.Sprintf("%f", p.Close))
				cur := p.Currency
				if cur == "" {
					cur = "EUR"
				}
				if err := s.queries.UpsertMarketData(ctx, db.UpsertMarketDataParams{
					SecurityISIN: sec.isin, Date: p.Date, Close: close, Currency: cur,
				}); err != nil {
					log.Printf("WARNING: upsert historical price %s %s: %v", sec.isin, p.Date.Format("2006-01-02"), err)
					continue
				}
				totalStored++
			}
		}
		pending = failed
	}

	if len(pending) > 0 {
		names := make([]string, len(pending))
		for i, s := range pending {
			names[i] = s.isin
		}
		log.Printf("ERROR: backfill prices: %d securities failed after %d attempts: %v", len(pending), maxRetries, names)
		return fmt.Errorf("%d/%d securities failed: %s", len(pending), totalSymbols, strings.Join(names, ", "))
	}
	log.Printf("backfill prices: stored %d historical price points for %d securities", totalStored, len(securities))
	return nil
}

// backfillHistoricalFXRates fetches full ECB FX history (since 1999) and stores it.
func (s *Scheduler) backfillHistoricalFXRates() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	rates, err := s.ecb.FetchHistoricalRates(ctx)
	if err != nil {
		log.Printf("ERROR: backfill FX rates: %v", err)
		return err
	}

	stored := 0
	for _, r := range rates {
		var rate pgtype.Numeric
		rate.Scan(fmt.Sprintf("%f", r.Rate))
		if err := s.queries.UpsertExchangeRate(ctx, db.UpsertExchangeRateParams{
			Date: r.Date, Currency: r.Currency, Rate: rate,
		}); err != nil {
			continue
		}
		stored++
	}
	if len(rates) > 0 && stored == 0 {
		return errors.New("failed to store historical FX rates")
	}
	log.Printf("backfill FX rates: stored %d historical rates", stored)
	return nil
}

func (s *Scheduler) BackfillNetWorthSnapshots() {
	if err := s.backfillNetWorthSnapshots(); err != nil {
		log.Printf("ERROR: backfill snapshots: %v", err)
	}
}

func (s *Scheduler) backfillNetWorthSnapshots() error {
	s.snapshotMu.Lock()
	defer s.snapshotMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	txns, err := s.queries.ListAllTransactions(ctx)
	if err != nil {
		return fmt.Errorf("list transactions: %w", err)
	}

	if len(txns) == 0 {
		return nil
	}

	// Sort transactions chronologically (they come DESC from the query)
	for i, j := 0, len(txns)-1; i < j; i, j = i+1, j-1 {
		txns[i], txns[j] = txns[j], txns[i]
	}

	accounts, _ := s.queries.ListAccounts(ctx)
	accTypes := make(map[uuid.UUID]string, len(accounts))
	for _, acc := range accounts {
		accTypes[acc.ID] = acc.Type
	}
	acctHasExplicitCash := make(map[uuid.UUID]bool)
	for _, txn := range txns {
		switch txn.Type {
		case "deposit", "withdrawal", "cash_transfer_in", "cash_transfer_out":
			acctHasExplicitCash[txn.AccountID] = true
		}
	}

	// Preload all price history into memory: map[isin][]pricePoint (sorted by date ASC)
	type pricePoint struct {
		date     time.Time
		price    float64
		currency string
	}
	priceHistory := make(map[string][]pricePoint)
	securityCurrency := make(map[string]string) // isin -> currency (e.g. "USD", "EUR")
	securities, err := s.queries.ListSecurities(ctx)
	if err == nil {
		for _, sec := range securities {
			securityCurrency[sec.ISIN] = sec.Currency
			rows, err := s.queries.ListPriceHistory(ctx, sec.ISIN)
			if err != nil || len(rows) == 0 {
				continue
			}
			pts := make([]pricePoint, 0, len(rows))
			for _, r := range rows {
				if r.Close.Valid {
					f, _ := r.Close.Float64Value()
					pts = append(pts, pricePoint{date: r.Date, price: f.Float64, currency: r.Currency})
				}
			}
			priceHistory[sec.ISIN] = pts
		}
	}
	log.Printf("backfill snapshots: loaded price history for %d securities", len(priceHistory))

	// Preload all FX rate history into memory: map[currency][]ratePoint (sorted by date ASC)
	type ratePoint struct {
		date time.Time
		rate float64 // ECB convention: 1 EUR = rate units of currency
	}
	fxHistory := make(map[string][]ratePoint)
	if fxRows, err := s.queries.ListAllExchangeRates(ctx); err == nil {
		for _, r := range fxRows {
			if r.Rate.Valid {
				f, _ := r.Rate.Float64Value()
				if f.Float64 > 0 {
					cur := strings.ToUpper(strings.TrimSpace(r.Currency))
					fxHistory[cur] = append(fxHistory[cur], ratePoint{date: r.Date, rate: f.Float64})
				}
			}
		}
	}
	if len(fxHistory) > 0 {
		log.Printf("backfill snapshots: loaded FX history for %d currencies", len(fxHistory))
	}

	// getPriceAt returns the most recent price and currency on or before the given date.
	// Falls back to 0 if no price is available.
	getPriceAt := func(isin string, date time.Time) (float64, string) {
		pts := priceHistory[isin]
		if len(pts) == 0 {
			return 0, ""
		}
		lo, hi := 0, len(pts)-1
		result := -1
		for lo <= hi {
			mid := (lo + hi) / 2
			if !pts[mid].date.After(date) {
				result = mid
				lo = mid + 1
			} else {
				hi = mid - 1
			}
		}
		if result >= 0 {
			return pts[result].price, pts[result].currency
		}
		return 0, ""
	}

	toEURAt := func(amount float64, currency string, date time.Time) float64 {
		c := strings.TrimSpace(currency)
		if c == "" || strings.EqualFold(c, "EUR") || amount == 0 {
			return amount
		}
		if c == "GBp" || c == "GBX" {
			amount /= 100.0
			c = "GBP"
		} else {
			c = strings.ToUpper(c)
		}
		pts := fxHistory[c]
		if len(pts) == 0 {
			return amount
		}
		lo, hi := 0, len(pts)-1
		result := -1
		for lo <= hi {
			mid := (lo + hi) / 2
			if !pts[mid].date.After(date) {
				result = mid
				lo = mid + 1
			} else {
				hi = mid - 1
			}
		}
		if result >= 0 {
			return amount / pts[result].rate
		}
		return amount / pts[0].rate
	}

	physical, err := assets.Load(ctx, s.queries)
	if err != nil {
		log.Printf("WARNING: backfill snapshots: load assets: %v", err)
	}

	// Replay transactions to build running state
	acctCash := make(map[uuid.UUID]float64)
	type holdingState struct {
		quantity  float64
		totalCost float64
	}
	holdings := make(map[string]*holdingState)

	// Build a map of transactions by date for efficient lookup
	type txnEvent struct {
		accID  uuid.UUID
		typ    string
		isin   string
		amtEUR float64
		feeEUR float64
		taxEUR float64
		qty    float64
	}
	txnsByDate := make(map[string][]txnEvent)
	for _, txn := range txns {
		dateKey := txn.Date.Format("2006-01-02")
		isin := ""
		if txn.SecurityISIN.Valid {
			isin = txn.SecurityISIN.String
		}
		txnsByDate[dateKey] = append(txnsByDate[dateKey], txnEvent{
			accID:  txn.AccountID,
			typ:    txn.Type,
			isin:   isin,
			amtEUR: toEURAt(numericToFloat(txn.Amount), txn.Currency, txn.Date),
			feeEUR: toEURAt(numericToFloat(txn.Fee), txn.Currency, txn.Date),
			taxEUR: toEURAt(numericToFloat(txn.Tax), txn.Currency, txn.Date),
			qty:    numericToFloat(txn.Quantity),
		})
	}

	// Pair transfer_out with matching transfer (in) for the same ISIN/qty
	// within 7 days, and drop both from the backfill. This prevents a
	// net-worth dip when securities are "in transit" between brokers —
	// the holdings persist untouched through the transit period.
	type transferKey struct {
		isin string
		qty  float64
	}
	transferOuts := make(map[transferKey]string)
	for dateKey, events := range txnsByDate {
		for _, ev := range events {
			if ev.typ == "transfer_out" && ev.isin != "" {
				transferOuts[transferKey{ev.isin, ev.qty}] = dateKey
			}
		}
	}
	type removal struct {
		date string
		isin string
		qty  float64
		typ  string
	}
	var removals []removal
	for dateKey, events := range txnsByDate {
		for _, ev := range events {
			if ev.typ == "transfer" && ev.isin != "" {
				key := transferKey{ev.isin, ev.qty}
				outDate, ok := transferOuts[key]
				if !ok {
					continue
				}
				outT, _ := time.Parse("2006-01-02", outDate)
				inT, _ := time.Parse("2006-01-02", dateKey)
				diff := inT.Sub(outT)
				if diff > 0 && diff <= 7*24*time.Hour {
					removals = append(removals, removal{outDate, ev.isin, ev.qty, "transfer_out"})
					removals = append(removals, removal{dateKey, ev.isin, ev.qty, "transfer"})
					delete(transferOuts, key)
				}
			}
		}
	}
	for _, r := range removals {
		events := txnsByDate[r.date]
		filtered := events[:0]
		removed := false
		for _, ev := range events {
			if !removed && ev.typ == r.typ && ev.isin == r.isin && ev.qty == r.qty {
				removed = true
				continue
			}
			filtered = append(filtered, ev)
		}
		txnsByDate[r.date] = filtered
	}

	// Iterate day-by-day from first transaction to yesterday in UTC.
	// Today's snapshot is handled by updateNetWorthSnapshot which uses
	// live prices and actual cash balances for higher accuracy.
	startDate := txns[0].Date
	start := time.Date(startDate.Year(), startDate.Month(), startDate.Day(), 0, 0, 0, 0, time.UTC)
	today := time.Now().UTC()
	end := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)

	snapshots := 0
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		dateKey := d.Format("2006-01-02")

		// Apply transactions for this day
		for _, ev := range txnsByDate[dateKey] {
			switch ev.typ {
			case "deposit", "interest", "cash_transfer_in":
				acctCash[ev.accID] += ev.amtEUR - ev.feeEUR - ev.taxEUR
			case "withdrawal", "fee", "tax", "cash_transfer_out":
				acctCash[ev.accID] -= ev.amtEUR + ev.feeEUR + ev.taxEUR
			case "transfer":
				if ev.isin == "" {
					acctCash[ev.accID] -= ev.amtEUR + ev.feeEUR + ev.taxEUR
				} else {
					h, ok := holdings[ev.isin]
					if !ok {
						h = &holdingState{}
						holdings[ev.isin] = h
					}
					h.quantity += ev.qty
					h.totalCost += ev.amtEUR
				}
			case "transfer_out":
				if ev.isin != "" {
					h, ok := holdings[ev.isin]
					if ok && h.quantity > 0 {
						sellQty := math.Min(ev.qty, h.quantity)
						costPerUnit := h.totalCost / h.quantity
						h.quantity -= sellQty
						h.totalCost -= costPerUnit * sellQty
						if h.quantity <= 1e-9 {
							delete(holdings, ev.isin)
						}
					}
				}
			case "buy", "savings_plan":
				acctCash[ev.accID] -= ev.amtEUR + ev.feeEUR + ev.taxEUR
				if ev.isin != "" {
					h, ok := holdings[ev.isin]
					if !ok {
						h = &holdingState{}
						holdings[ev.isin] = h
					}
					h.quantity += ev.qty
					h.totalCost += ev.amtEUR + ev.feeEUR
				}
			case "sell":
				acctCash[ev.accID] += ev.amtEUR - ev.feeEUR - ev.taxEUR
				if ev.isin != "" {
					h, ok := holdings[ev.isin]
					if ok && h.quantity > 0 {
						sellQty := math.Min(ev.qty, h.quantity)
						costPerUnit := h.totalCost / h.quantity
						h.quantity -= sellQty
						h.totalCost -= costPerUnit * sellQty
						if h.quantity <= 1e-9 {
							delete(holdings, ev.isin)
						}
					}
				}
			case "dividend":
				acctCash[ev.accID] += ev.amtEUR - ev.feeEUR - ev.taxEUR
			}
		}

		cashBalance := 0.0
		for accID, bal := range acctCash {
			if bal < 0 && !acctHasExplicitCash[accID] && isSecuritiesOnlyAccountType(accTypes[accID]) {
				bal = 0
			}
			cashBalance += bal
		}

		// Compute investment value using market prices, converting to EUR
		investmentValue := 0.0
		for isin, h := range holdings {
			if h.quantity <= 1e-9 {
				continue
			}
			price, priceCur := getPriceAt(isin, d)
			if price <= 0 {
				// Fall back to cost basis (already in EUR from transaction amounts)
				investmentValue += h.totalCost
				continue
			}
			cur := priceCur
			if cur == "" {
				cur = securityCurrency[isin]
			}
			investmentValue += toEURAt(h.quantity*price, cur, d)
		}

		physicalValue := physical.PhysicalAt(d)
		debt := physical.DebtAt(d)
		total := cashBalance + investmentValue + physicalValue - debt

		var totalNum, cashNum, investNum, physicalNum, debtNum pgtype.Numeric
		totalNum.Scan(fmt.Sprintf("%.4f", total))
		cashNum.Scan(fmt.Sprintf("%.4f", cashBalance))
		investNum.Scan(fmt.Sprintf("%.4f", investmentValue))
		physicalNum.Scan(fmt.Sprintf("%.4f", physicalValue))
		debtNum.Scan(fmt.Sprintf("%.4f", debt))

		if err := s.queries.UpsertNetWorthSnapshot(ctx, db.UpsertNetWorthSnapshotParams{
			Date: d, Total: totalNum, CashComponent: cashNum, InvestmentComponent: investNum,
			PhysicalComponent: physicalNum, DebtComponent: debtNum,
		}); err != nil {
			log.Printf("WARNING: backfill snapshot %s: %v", d.Format("2006-01-02"), err)
		}
		snapshots++
	}

	log.Printf("backfill snapshots: %d daily snapshots computed", snapshots)
	return nil
}

// convertToEURScheduler converts an amount from the given currency to EUR using ECB rates.
// ECB rates are EUR→currency (1 EUR = X currency), so amountEUR = amount / rate.
func convertToEURScheduler(ctx context.Context, q *db.Queries, amount float64, currency string) float64 {
	c := strings.TrimSpace(currency)
	if c == "" || strings.EqualFold(c, "EUR") || amount == 0 {
		return amount
	}
	if c == "GBp" || c == "GBX" {
		amount /= 100.0
		c = "GBP"
	} else {
		c = strings.ToUpper(c)
	}
	rateRow, err := q.GetLatestExchangeRate(ctx, c)
	if err != nil {
		return amount
	}
	rate := numericToFloat(rateRow.Rate)
	if rate <= 0 {
		return amount
	}
	return amount / rate
}

func numericToFloat(n pgtype.Numeric) float64 {
	if !n.Valid {
		return 0
	}
	f, _ := n.Float64Value()
	if math.IsNaN(f.Float64) || math.IsInf(f.Float64, 0) {
		return 0
	}
	return f.Float64
}

// evaluateAlerts checks all active price alerts against current prices/portfolio value.
func (s *Scheduler) evaluateAlerts() {
	if !s.tryStartJob("alerts") {
		return
	}
	defer s.finishJob("alerts")

	s.setJobStatus("alerts", "running", "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	alerts, err := s.queries.ListActivePriceAlerts(ctx)
	if err != nil {
		s.setJobStatus("alerts", "error", err.Error())
		log.Printf("evaluate alerts: list: %v", err)
		return
	}
	if len(alerts) == 0 {
		s.setJobStatus("alerts", "ok", "no active alerts")
		return
	}

	// Load latest prices converted to EUR
	prices, _ := s.queries.ListLatestPrices(ctx)
	priceMap := make(map[string]float64)
	for _, p := range prices {
		if p.Close.Valid {
			f, _ := p.Close.Float64Value()
			priceMap[p.SecurityISIN] = convertToEURScheduler(ctx, s.queries, f.Float64, p.Currency)
		}
	}

	// Cooldown map from recent notifications so daily_change or failed toggles cannot spam
	existingNotifs, _ := s.queries.ListNotifications(ctx, 200)
	recentFired := make(map[uuid.UUID]bool)
	now := time.Now()
	for _, n := range existingNotifs {
		if n.AlertID != nil && now.Sub(n.TriggeredAt) < 20*time.Hour {
			recentFired[*n.AlertID] = true
		}
	}

	// Get current net worth
	snaps, _ := s.queries.ListNetWorthSnapshots(ctx, 1)
	netWorth := 0.0
	if len(snaps) > 0 && snaps[0].Total.Valid {
		f, _ := snaps[0].Total.Float64Value()
		netWorth = f.Float64
	}

	triggered := 0
	for _, alert := range alerts {
		if recentFired[alert.ID] {
			continue
		}
		threshold := numericToFloat(alert.Threshold)
		var shouldTrigger bool
		var deactivateAfter bool
		var message string
		var value float64

		name := ""
		if alert.SecurityISIN.Valid {
			name = alert.SecurityISIN.String
		}
		if alert.SecurityName.Valid && alert.SecurityName.String != "" {
			name = alert.SecurityName.String
		}

		switch alert.AlertType {
		case "price_above":
			if alert.SecurityISIN.Valid {
				if price, ok := priceMap[alert.SecurityISIN.String]; ok {
					value = price
					if price >= threshold {
						shouldTrigger = true
						deactivateAfter = true
						message = fmt.Sprintf("%s price reached %.2f EUR (threshold: %.2f EUR)", name, price, threshold)
					}
				}
			}
		case "price_below":
			if alert.SecurityISIN.Valid {
				if price, ok := priceMap[alert.SecurityISIN.String]; ok {
					value = price
					if price <= threshold {
						shouldTrigger = true
						deactivateAfter = true
						message = fmt.Sprintf("%s price dropped to %.2f EUR (threshold: %.2f EUR)", name, price, threshold)
					}
				}
			}
		case "daily_change":
			if alert.SecurityISIN.Valid {
				rows, err := s.queries.ListPriceHistory(ctx, alert.SecurityISIN.String)
				if err == nil && len(rows) >= 2 {
					prevRow := rows[len(rows)-2]
					currRow := rows[len(rows)-1]
					todayUTC := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
					currDateUTC := time.Date(currRow.Date.Year(), currRow.Date.Month(), currRow.Date.Day(), 0, 0, 0, 0, time.UTC)
					if todayUTC.Sub(currDateUTC) <= 24*time.Hour {
						prev := convertToEURScheduler(ctx, s.queries, numericToFloat(prevRow.Close), prevRow.Currency)
						curr := convertToEURScheduler(ctx, s.queries, numericToFloat(currRow.Close), currRow.Currency)
						if prev > 0 {
							pctChange := ((curr - prev) / prev) * 100
							value = math.Abs(pctChange)
							if math.Abs(pctChange) >= threshold {
								shouldTrigger = true
								sign := "+"
								if pctChange < 0 {
									sign = ""
								}
								message = fmt.Sprintf("%s moved %s%.2f%% today (threshold: %.2f%%)", name, sign, pctChange, threshold)
							}
						}
					}
				}
			}
		case "portfolio_milestone":
			value = netWorth
			if netWorth >= threshold {
				shouldTrigger = true
				deactivateAfter = true
				message = fmt.Sprintf("Net worth reached %s EUR (milestone: %s EUR)", fmtEUR(netWorth), fmtEUR(threshold))
			}
		}

		if shouldTrigger {
			var val pgtype.Numeric
			val.Scan(fmt.Sprintf("%.2f", value))
			s.notifyAndDispatch(ctx, alert.ID, message, val)
			if deactivateAfter {
				if err := s.queries.TogglePriceAlert(ctx, alert.ID); err != nil {
					log.Printf("WARNING: deactivate triggered alert %s: %v", alert.ID, err)
				}
			}
			recentFired[alert.ID] = true
			triggered++
		}
	}

	msg := fmt.Sprintf("%d/%d alerts triggered", triggered, len(alerts))
	s.setJobStatus("alerts", "ok", msg)
	if triggered > 0 {
		log.Printf("evaluate alerts: %s", msg)
	}
}

// checkTaxLossHarvesting scans for positions with significant unrealized losses
// and creates notifications for tax-loss harvesting opportunities.
func (s *Scheduler) checkTaxLossHarvesting() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	holdings, err := s.queries.ListCurrentHoldings(ctx)
	if err != nil || len(holdings) == 0 {
		return
	}

	prices, _ := s.queries.ListLatestPrices(ctx)
	priceMap := make(map[string]float64)
	for _, p := range prices {
		if p.Close.Valid {
			f, _ := p.Close.Float64Value()
			priceMap[p.SecurityISIN] = convertToEURScheduler(ctx, s.queries, f.Float64, p.Currency)
		}
	}

	secs, _ := s.queries.ListSecurities(ctx)
	secNames := make(map[string]string, len(secs))
	secTFS := make(map[string]float64, len(secs))
	for _, sec := range secs {
		secNames[sec.ISIN] = sec.Name
		var eqPtr *float64
		if sec.EquityPct.Valid {
			v := numericToFloat(sec.EquityPct)
			eqPtr = &v
		}
		secTFS[sec.ISIN] = analytics.ResolveSecurityTFSRate(sec.AssetClass, sec.Name, eqPtr)
	}

	// Aggregate holdings across depots per ISIN in EUR
	type aggHold struct {
		costEUR    float64
		valEUR     float64
		assetClass string
	}
	byISIN := make(map[string]*aggHold)
	for _, h := range holdings {
		qty := numericToFloat(h.Quantity)
		avgCost := numericToFloat(h.AvgCostBasis)
		priceEUR := priceMap[h.SecurityISIN]
		if qty <= 0 || avgCost <= 0 || priceEUR <= 0 {
			continue
		}
		costEUR := convertToEURScheduler(ctx, s.queries, qty*avgCost, h.Currency)
		valEUR := qty * priceEUR
		a := byISIN[h.SecurityISIN]
		if a == nil {
			a = &aggHold{assetClass: h.AssetClass}
			byISIN[h.SecurityISIN] = a
		}
		a.costEUR += costEUR
		a.valEUR += valEUR
	}

	existing, _ := s.queries.ListNotifications(ctx, 100)
	cutoff := time.Now().AddDate(0, 0, -7)

	// Check each holding for significant unrealized loss (>5% and >100 EUR)
	for isin, a := range byISIN {
		if a.costEUR <= 0 {
			continue
		}
		loss := a.valEUR - a.costEUR
		lossPct := loss / a.costEUR * 100

		if loss < -100 && lossPct < -5 {
			name := secNames[isin]
			if name == "" {
				name = isin
			}
			tfs := secTFS[isin]
			taxSaving := math.Abs(loss) * (1 - tfs) * analytics.EffectiveTaxRate
			isinTag := "(" + isin + ")"
			msg := fmt.Sprintf("Tax-loss harvesting: %s %s is down %s EUR (%.1f%%). Selling could save ~%s EUR in tax.", name, isinTag, fmtEUR(loss), lossPct, fmtEUR(taxSaving))

			alreadyNotified := false
			for _, n := range existing {
				if strings.HasPrefix(n.Message, "Tax-loss harvesting:") && n.TriggeredAt.After(cutoff) {
					if strings.Contains(n.Message, isinTag) || strings.HasPrefix(n.Message, "Tax-loss harvesting: "+name+" is down") {
						alreadyNotified = true
						break
					}
				}
			}
			if alreadyNotified {
				continue
			}

			lossValue := pgtype.Numeric{}
			lossValue.Scan(fmt.Sprintf("%.2f", math.Abs(loss)))
			s.notifyAndDispatch(ctx, uuid.Nil, msg, lossValue)
			log.Printf("tax-loss harvesting alert: %s (%.0f EUR loss)", name, loss)
		}
	}
}

func (s *Scheduler) generateMonthlyReport() {
	if !s.tryStartJob("report-monthly") {
		return
	}
	defer s.finishJob("report-monthly")

	s.setJobStatus("report", "running", "")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(start.Year(), start.Month()+1, 0, 0, 0, 0, 0, time.UTC)
	label := start.Format("2006-01")

	if exists, _ := s.queries.ReportExistsForPeriod(ctx, db.ReportExistsForPeriodParams{ReportType: "monthly", PeriodLabel: label}); exists {
		s.setJobStatus("report", "ok", "already exists: "+label)
		return
	}

	data := s.computeReportData(ctx, start, end)
	dataJSON, _ := json.Marshal(data)
	if err := s.queries.InsertWealthReport(ctx, db.InsertWealthReportParams{
		ReportType: "monthly", PeriodLabel: label, PeriodStart: start, PeriodEnd: end, Data: dataJSON,
	}); err != nil {
		s.setJobStatus("report", "error", err.Error())
		return
	}
	s.setJobStatus("report", "ok", "monthly "+label)
	log.Printf("generated monthly report: %s", label)

	// Send monthly advisor digest to configured channels
	var digestData map[string]any
	json.Unmarshal(dataJSON, &digestData)
	s.sendMonthlyDigest(ctx, digestData, label)
}

// sendMonthlyDigest creates and dispatches a digest to configured channels.
func (s *Scheduler) sendMonthlyDigest(ctx context.Context, data map[string]any, label string) {
	subject := fmt.Sprintf("Wealth Monthly Digest — %s", label)

	// Extract fields safely
	nwEnd, _ := data["net_worth_end"].(float64)
	nwChange, _ := data["net_worth_change"].(float64)
	nwChangePct, _ := data["net_worth_change_pct"].(float64)
	divs, _ := data["total_dividends"].(float64)

	var lines []string
	lines = append(lines, fmt.Sprintf("Net Worth: %s EUR", fmtEUR(nwEnd)))
	if nwChange != 0 {
		sign := "+"
		if nwChange < 0 {
			sign = ""
		}
		lines = append(lines, fmt.Sprintf("Monthly Change: %s%s EUR (%s%.1f%%)", sign, fmtEUR(nwChange), sign, nwChangePct))
	}
	if divs > 0 {
		lines = append(lines, fmt.Sprintf("Dividend Income: %s EUR", fmtEUR(divs)))
	}

	if topGainer, ok := data["top_gainer"].(map[string]any); ok {
		name, _ := topGainer["name"].(string)
		retPct, _ := topGainer["return_pct"].(float64)
		if name != "" {
			lines = append(lines, fmt.Sprintf("Top Gainer: %s (+%.1f%%)", name, retPct))
		}
	}
	if topLoser, ok := data["top_loser"].(map[string]any); ok {
		name, _ := topLoser["name"].(string)
		retPct, _ := topLoser["return_pct"].(float64)
		if name != "" {
			lines = append(lines, fmt.Sprintf("Top Loser: %s (%.1f%%)", name, retPct))
		}
	}

	body := strings.Join(lines, "\n")
	// Dispatch to monthly channels
	s.dispatchMessage(ctx, "digest", subject, body, "monthly")
	// Also dispatch to quarterly channels on Jan/Apr/Jul/Oct
	month := time.Now().UTC().Month()
	if month == time.January || month == time.April || month == time.July || month == time.October {
		s.dispatchMessage(ctx, "digest", subject, body, "quarterly")
	}
	log.Printf("monthly digest dispatched for %s", label)
}

func (s *Scheduler) generateAnnualReport() {
	if !s.tryStartJob("report-annual") {
		return
	}
	defer s.finishJob("report-annual")

	s.setJobStatus("report", "running", "")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	year := time.Now().UTC().Year() - 1
	label := fmt.Sprintf("%d", year)
	start := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC)

	if exists, _ := s.queries.ReportExistsForPeriod(ctx, db.ReportExistsForPeriodParams{ReportType: "annual", PeriodLabel: label}); exists {
		s.setJobStatus("report", "ok", "already exists: "+label)
		return
	}

	data := s.computeReportData(ctx, start, end)
	dataJSON, _ := json.Marshal(data)
	if err := s.queries.InsertWealthReport(ctx, db.InsertWealthReportParams{
		ReportType: "annual", PeriodLabel: label, PeriodStart: start, PeriodEnd: end, Data: dataJSON,
	}); err != nil {
		s.setJobStatus("report", "error", err.Error())
		return
	}
	s.setJobStatus("report", "ok", "annual "+label)
	log.Printf("generated annual report: %s", label)
}

func (s *Scheduler) backfillReports() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Find earliest snapshot date
	snaps, _ := s.queries.ListNetWorthSnapshots(ctx, 5000)
	if len(snaps) < 2 {
		return
	}
	earliest := snaps[len(snaps)-1].Date
	now := time.Now().UTC()

	// Backfill monthly reports from earliest snapshot month to last month
	cur := time.Date(earliest.Year(), earliest.Month(), 1, 0, 0, 0, 0, time.UTC)
	lastMonth := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, time.UTC)
	count := 0
	for !cur.After(lastMonth) {
		label := cur.Format("2006-01")
		if exists, _ := s.queries.ReportExistsForPeriod(ctx, db.ReportExistsForPeriodParams{ReportType: "monthly", PeriodLabel: label}); !exists {
			start := cur
			end := time.Date(cur.Year(), cur.Month()+1, 0, 0, 0, 0, 0, time.UTC)
			data := s.computeReportData(ctx, start, end)
			dataJSON, _ := json.Marshal(data)
			if err := s.queries.InsertWealthReport(ctx, db.InsertWealthReportParams{
				ReportType: "monthly", PeriodLabel: label, PeriodStart: start, PeriodEnd: end, Data: dataJSON,
			}); err == nil {
				count++
			}
		}
		cur = time.Date(cur.Year(), cur.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	}

	// Backfill annual reports from earliest year to last year
	for year := earliest.Year(); year < now.Year(); year++ {
		label := fmt.Sprintf("%d", year)
		if exists, _ := s.queries.ReportExistsForPeriod(ctx, db.ReportExistsForPeriodParams{ReportType: "annual", PeriodLabel: label}); !exists {
			start := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
			end := time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC)
			data := s.computeReportData(ctx, start, end)
			dataJSON, _ := json.Marshal(data)
			if err := s.queries.InsertWealthReport(ctx, db.InsertWealthReportParams{
				ReportType: "annual", PeriodLabel: label, PeriodStart: start, PeriodEnd: end, Data: dataJSON,
			}); err == nil {
				count++
			}
		}
	}

	if count > 0 {
		log.Printf("backfilled %d missing reports", count)
	}
}

func (s *Scheduler) computeReportData(ctx context.Context, start, end time.Time) map[string]any {
	snaps, _ := s.queries.ListNetWorthSnapshots(ctx, 5000)
	nwStart, nwEnd := 0.0, 0.0
	for _, snap := range snaps {
		val := numericToFloat(snap.Total)
		if !snap.Date.Before(start) && !snap.Date.After(end) {
			if nwEnd == 0 {
				nwEnd = val
			}
			nwStart = val
		}
	}
	change := nwEnd - nwStart
	changePct := 0.0
	if nwStart > 0 {
		changePct = (change / nwStart) * 100
	}

	txns, _ := s.queries.ListAllTransactions(ctx)
	fxRates, _ := s.queries.ListAllExchangeRates(ctx)
	type fxPt struct {
		date time.Time
		rate float64
	}
	fxByCur := make(map[string][]fxPt)
	for _, r := range fxRates {
		if r.Rate.Valid {
			f, _ := r.Rate.Float64Value()
			if f.Float64 > 0 {
				cur := strings.ToUpper(strings.TrimSpace(r.Currency))
				fxByCur[cur] = append(fxByCur[cur], fxPt{date: r.Date, rate: f.Float64})
			}
		}
	}
	toEURAt := func(amount float64, cur string, dt time.Time) float64 {
		c := strings.TrimSpace(cur)
		if c == "" || strings.EqualFold(c, "EUR") || amount == 0 {
			return amount
		}
		if c == "GBp" || c == "GBX" {
			amount /= 100.0
			c = "GBP"
		} else {
			c = strings.ToUpper(c)
		}
		pts := fxByCur[c]
		if len(pts) == 0 {
			return amount
		}
		lo, hi, res := 0, len(pts)-1, -1
		for lo <= hi {
			mid := (lo + hi) / 2
			if !pts[mid].date.After(dt) {
				res = mid
				lo = mid + 1
			} else {
				hi = mid - 1
			}
		}
		if res >= 0 {
			return amount / pts[res].rate
		}
		return amount / pts[0].rate
	}

	newTxns, dividends := 0, 0.0
	for _, txn := range txns {
		if txn.Date.Before(start) || txn.Date.After(end) {
			continue
		}
		newTxns++
		if txn.Type == "dividend" && txn.Amount.Valid {
			f, _ := txn.Amount.Float64Value()
			dividends += toEURAt(f.Float64, txn.Currency, txn.Date)
		}
	}

	type replayHold struct {
		qty  float64
		cost float64
	}
	replayed := make(map[string]*replayHold)
	for i := len(txns) - 1; i >= 0; i-- {
		txn := txns[i]
		if txn.Date.After(end) || !txn.SecurityISIN.Valid || txn.SecurityISIN.String == "" {
			continue
		}
		isin := txn.SecurityISIN.String
		qty := numericToFloat(txn.Quantity)
		amtEUR := toEURAt(numericToFloat(txn.Amount), txn.Currency, txn.Date)
		feeEUR := toEURAt(numericToFloat(txn.Fee), txn.Currency, txn.Date)

		switch txn.Type {
		case "buy", "savings_plan":
			if qty > 0 {
				rh := replayed[isin]
				if rh == nil {
					rh = &replayHold{}
					replayed[isin] = rh
				}
				rh.qty += qty
				rh.cost += amtEUR + feeEUR
			}
		case "transfer":
			if qty > 0 {
				rh := replayed[isin]
				if rh == nil {
					rh = &replayHold{}
					replayed[isin] = rh
				}
				rh.qty += qty
				if amtEUR > 0 {
					rh.cost += amtEUR
				}
			}
		case "sell", "transfer_out":
			if rh := replayed[isin]; rh != nil && rh.qty > 0 && qty > 0 {
				sellQty := math.Min(qty, rh.qty)
				avg := rh.cost / rh.qty
				rh.qty -= sellQty
				rh.cost -= avg * sellQty
				if rh.qty <= 1e-9 {
					delete(replayed, isin)
				}
			}
		}
	}

	secs, _ := s.queries.ListSecurities(ctx)
	secNames := make(map[string]string, len(secs))
	for _, sec := range secs {
		secNames[sec.ISIN] = sec.Name
	}

	type reportHoldingItem struct {
		ISIN      string  `json:"isin"`
		Name      string  `json:"name"`
		Value     float64 `json:"value"`
		Weight    float64 `json:"weight"`
		ReturnPct float64 `json:"return_pct,omitempty"`
	}
	type holdAgg struct {
		isin, name  string
		value, cost float64
	}
	agg := make(map[string]*holdAgg)
	totalValue := 0.0
	for isin, rh := range replayed {
		if rh.qty <= 1e-9 {
			continue
		}
		value := rh.cost
		if priceRow, err := s.queries.GetPriceAtDate(ctx, db.GetPriceAtDateParams{
			SecurityISIN: isin,
			Date:         end,
		}); err == nil && priceRow.Close.Valid {
			f, _ := priceRow.Close.Float64Value()
			if f.Float64 > 0 {
				value = toEURAt(rh.qty*f.Float64, priceRow.Currency, end)
			}
		}
		name := secNames[isin]
		if name == "" {
			name = isin
		}
		agg[isin] = &holdAgg{isin: isin, name: name, value: value, cost: rh.cost}
		totalValue += value
	}

	holdingsList := make([]reportHoldingItem, 0, len(agg))
	var topGainer, topLoser *reportHoldingItem
	for _, a := range agg {
		weight := 0.0
		if totalValue > 0 {
			weight = (a.value / totalValue) * 100
		}
		retPct := 0.0
		if a.cost > 0 {
			retPct = ((a.value - a.cost) / a.cost) * 100
		}
		item := reportHoldingItem{
			ISIN:      a.isin,
			Name:      a.name,
			Value:     math.Round(a.value*100) / 100,
			Weight:    math.Round(weight*10) / 10,
			ReturnPct: math.Round(retPct*10) / 10,
		}
		holdingsList = append(holdingsList, item)
		if topGainer == nil || retPct > topGainer.ReturnPct {
			g := item
			topGainer = &g
		}
		if topLoser == nil || retPct < topLoser.ReturnPct {
			l := item
			topLoser = &l
		}
	}
	sort.Slice(holdingsList, func(i, j int) bool {
		if holdingsList[i].Value != holdingsList[j].Value {
			return holdingsList[i].Value > holdingsList[j].Value
		}
		return holdingsList[i].ISIN < holdingsList[j].ISIN
	})

	out := map[string]any{
		"net_worth_start":      math.Round(nwStart*100) / 100,
		"net_worth_end":        math.Round(nwEnd*100) / 100,
		"net_worth_change":     math.Round(change*100) / 100,
		"net_worth_change_pct": math.Round(changePct*10) / 10,
		"total_dividends":      math.Round(dividends*100) / 100,
		"new_transactions":     newTxns,
		"holdings":             holdingsList,
	}
	if topGainer != nil {
		out["top_gainer"] = topGainer
	}
	if topLoser != nil {
		out["top_loser"] = topLoser
	}
	return out
}

// checkTaxCalendarReminders generates notifications for upcoming tax events.
func (s *Scheduler) checkTaxCalendarReminders() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	year := today.Year()

	type taxEvent struct {
		date    time.Time
		title   string
		message string
		amount  float64
	}

	var events []taxEvent

	// Vorabpauschale (Jan 2)
	events = append(events, taxEvent{
		date:  time.Date(year, 1, 2, 0, 0, 0, 0, time.UTC),
		title: "Vorabpauschale",
	})

	// FSA Review (Jan 15)
	events = append(events, taxEvent{
		date:    time.Date(year, 1, 15, 0, 0, 0, 0, time.UTC),
		title:   "Freistellungsauftrag",
		message: "Prüfen Sie, ob Ihr FSA (1.000 EUR) optimal auf Ihre Depots verteilt ist.",
	})

	// Steuererklärung (Jul 31)
	events = append(events, taxEvent{
		date:    time.Date(year, 7, 31, 0, 0, 0, 0, time.UTC),
		title:   "Steuererklärung",
		message: fmt.Sprintf("Einkommensteuererklärung %d einreichen (Frist: 31.07.%d).", year-1, year),
	})

	// Verlustbescheinigung (Dec 15)
	events = append(events, taxEvent{
		date:    time.Date(year, 12, 15, 0, 0, 0, 0, time.UTC),
		title:   "Verlustbescheinigung",
		message: "Verlustbescheinigung bei der Bank beantragen (Frist: 15.12.).",
	})

	// Contribution Routing Review (Jan 10)
	events = append(events, taxEvent{
		date:    time.Date(year, 1, 10, 0, 0, 0, 0, time.UTC),
		title:   "Contribution Review",
		message: fmt.Sprintf("Annual contribution routing review for %d: Check bAV match, Riester Zulagen, Rürup deductions, and emergency reserve. Review in Planning tab.", year),
	})

	// Annual Playbook Review (Jan 5)
	events = append(events, taxEvent{
		date:    time.Date(year, 1, 5, 0, 0, 0, 0, time.UTC),
		title:   "Playbook Review",
		message: fmt.Sprintf("Annual Financial Independence Playbook review for %d: Compare last year's plan vs actual results. Check new opportunities and carry forward deferred actions. Review in Planning tab.", year),
	})

	// Annual Protection Review (Nov 1)
	events = append(events, taxEvent{
		date:    time.Date(year, 11, 1, 0, 0, 0, 0, time.UTC),
		title:   "Protection Review",
		message: fmt.Sprintf("Annual insurance protection review for %d: Review coverage gaps, renewal dates, and over-insurance. Check Insurance Inventory in Planning tab.", year),
	})

	// Vorabpauschale due on Jan 2 of `year` applies to the preceding tax year (`year - 1`)
	vpTax := s.computeSchedulerVorabpauschaleTax(ctx, year-1)
	if vpTax > 10 {
		events[0].message = fmt.Sprintf("Vorabpauschale: ~%s EUR fällig am 02.01. Verrechnungskonto prüfen.", fmtEUR(vpTax))
		events[0].amount = vpTax
	} else {
		events[0].message = "Vorabpauschale: Kein relevanter Betrag für dieses Jahr."
	}

	// Check each event: if within 14 days, create notification (if not already sent)
	existing, _ := s.queries.ListNotifications(ctx, 100)
	for _, ev := range events {
		daysUntil := int(ev.date.Sub(today).Hours() / 24)
		if daysUntil < -1 || daysUntil > 14 {
			continue
		}

		// Check if already notified this month for this event
		alreadySent := false
		prefix := fmt.Sprintf("[%s]", ev.title)
		for _, n := range existing {
			nt := n.TriggeredAt.UTC()
			if strings.HasPrefix(n.Message, prefix) && nt.Month() == today.Month() && nt.Year() == today.Year() {
				alreadySent = true
				break
			}
		}
		if alreadySent {
			continue
		}

		msg := fmt.Sprintf("[%s] %s", ev.title, ev.message)
		val := pgtype.Numeric{}
		if ev.amount > 0 {
			val.Scan(fmt.Sprintf("%.2f", ev.amount))
		}
		s.notifyAndDispatch(ctx, uuid.Nil, msg, val)
		log.Printf("tax calendar reminder: %s (in %d days)", ev.title, daysUntil)
	}

	// Also check deposit insurance thresholds
	s.checkDepositInsurance()
}

// checkDepositInsurance creates notifications when per-institution cash exceeds 80% of 100K limit.
func (s *Scheduler) checkDepositInsurance() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	accounts, err := s.queries.ListAccounts(ctx)
	if err != nil {
		return
	}

	// Sum cash per institution in EUR
	instCash := make(map[string]float64)
	if cashRows, err := s.queries.ListAllAccountCashByCurrency(ctx); err == nil {
		accCashEUR := make(map[uuid.UUID]float64)
		accExplicit := make(map[uuid.UUID]bool)
		for _, row := range cashRows {
			accCashEUR[row.AccountID] += convertToEURScheduler(ctx, s.queries, numericToFloat(row.Balance), row.Currency)
			if row.HasExplicitCash {
				accExplicit[row.AccountID] = true
			}
		}
		for _, acc := range accounts {
			if acc.Type != "checking" && acc.Type != "savings" && acc.Type != "brokerage" {
				continue
			}
			bal := accCashEUR[acc.ID]
			if bal < 0 && !accExplicit[acc.ID] && isSecuritiesOnlyAccountType(acc.Type) {
				bal = 0
			}
			if bal > 0 {
				instCash[acc.Institution] += bal
			}
		}
	} else {
		for _, acc := range accounts {
			if acc.Type == "checking" || acc.Type == "savings" || acc.Type == "brokerage" {
				bal, err := s.queries.GetCashBalance(ctx, acc.ID)
				if err == nil && bal.Valid {
					f, _ := bal.Float64Value()
					balEUR := convertToEURScheduler(ctx, s.queries, f.Float64, acc.Currency)
					if balEUR > 0 {
						instCash[acc.Institution] += balEUR
					}
				}
			}
		}
	}

	// Check thresholds
	const limit = 100000.0
	existing, _ := s.queries.ListNotifications(ctx, 50)
	now := time.Now().UTC()

	for inst, cash := range instCash {
		if cash < limit*0.8 {
			continue
		}

		title := "Deposit Insurance"
		// Deduplicate: skip if already notified this month
		alreadySent := false
		prefix := fmt.Sprintf("[%s] %s", title, inst)
		for _, n := range existing {
			nt := n.TriggeredAt.UTC()
			if strings.HasPrefix(n.Message, prefix) && nt.Month() == now.Month() && nt.Year() == now.Year() {
				alreadySent = true
				break
			}
		}
		if alreadySent {
			continue
		}

		pct := cash / limit * 100
		var msg string
		if cash >= limit {
			msg = fmt.Sprintf("[%s] %s: %s EUR exceeds 100.000 EUR Einlagensicherung limit (%.0f%%). Consider distributing across institutions.", title, inst, fmtEUR(cash), pct)
		} else {
			msg = fmt.Sprintf("[%s] %s: %s EUR approaching 100.000 EUR limit (%.0f%%). Monitor deposits.", title, inst, fmtEUR(cash), pct)
		}
		val := pgtype.Numeric{}
		val.Scan(fmt.Sprintf("%.2f", cash))
		s.notifyAndDispatch(ctx, uuid.Nil, msg, val)
		log.Printf("deposit insurance alert: %s (%.0f EUR, %.0f%%)", inst, cash, pct)
	}
}

// loadEnrichedHoldingsForScheduler is a simplified version for the scheduler context.
type schedulerHolding struct {
	isin  string
	value float64
}

func loadEnrichedHoldingsForScheduler(ctx context.Context, q *db.Queries) ([]schedulerHolding, error) {
	holdings, err := q.ListCurrentHoldings(ctx)
	if err != nil {
		return nil, err
	}
	prices, err := q.ListLatestPrices(ctx)
	if err != nil {
		return nil, err
	}
	pm := make(map[string]float64)
	for _, p := range prices {
		if p.Close.Valid {
			f, _ := p.Close.Float64Value()
			pm[p.SecurityISIN] = convertToEURScheduler(ctx, q, f.Float64, p.Currency)
		}
	}
	var result []schedulerHolding
	for _, h := range holdings {
		qty := numericToFloat(h.Quantity)
		priceEUR := pm[h.SecurityISIN]
		if qty > 0 && priceEUR > 0 {
			result = append(result, schedulerHolding{isin: h.SecurityISIN, value: qty * priceEUR})
		}
	}
	return result, nil
}

// computeSchedulerVorabpauschaleTax computes total § 18 InvStG Vorabpauschale tax
// for `taxYear` using FIFO-reconstructed Jan 1 / mid-year lots, distributions,
// appreciation cap, and per-security Teilfreistellung rates.
func (s *Scheduler) computeSchedulerVorabpauschaleTax(ctx context.Context, taxYear int) float64 {
	if basiszins, ok := analytics.BasiszinsByYear[taxYear]; !ok || basiszins <= 0 {
		return 0
	}

	secs, err := s.queries.ListSecurities(ctx)
	if err != nil || len(secs) == 0 {
		return 0
	}
	secMap := make(map[string]db.Security, len(secs))
	tfsMap := make(map[string]float64, len(secs))
	for _, sec := range secs {
		secMap[sec.ISIN] = sec
		var eqPtr *float64
		if sec.EquityPct.Valid {
			v := numericToFloat(sec.EquityPct)
			eqPtr = &v
		}
		tfsMap[sec.ISIN] = analytics.ResolveSecurityTFSRate(sec.AssetClass, sec.Name, eqPtr)
	}

	txns, err := s.queries.ListAllTransactions(ctx)
	if err != nil || len(txns) == 0 {
		return 0
	}

	type fxPt struct {
		date time.Time
		rate float64
	}
	fxByCur := make(map[string][]fxPt)
	if fxRows, err := s.queries.ListAllExchangeRates(ctx); err == nil {
		for _, r := range fxRows {
			if r.Rate.Valid {
				if f := numericToFloat(r.Rate); f > 0 {
					cur := strings.ToUpper(strings.TrimSpace(r.Currency))
					fxByCur[cur] = append(fxByCur[cur], fxPt{date: r.Date, rate: f})
				}
			}
		}
	}
	toEURAt := func(amount float64, currency string, dt time.Time) float64 {
		c := strings.TrimSpace(currency)
		if c == "" || strings.EqualFold(c, "EUR") || amount == 0 {
			return amount
		}
		if c == "GBp" || c == "GBX" {
			amount /= 100.0
			c = "GBP"
		} else {
			c = strings.ToUpper(c)
		}
		pts := fxByCur[c]
		if len(pts) == 0 {
			return convertToEURScheduler(ctx, s.queries, amount, c)
		}
		lo, hi, res := 0, len(pts)-1, -1
		for lo <= hi {
			mid := (lo + hi) / 2
			if !pts[mid].date.After(dt) {
				res = mid
				lo = mid + 1
			} else {
				hi = mid - 1
			}
		}
		if res >= 0 {
			return amount / pts[res].rate
		}
		return amount / pts[0].rate
	}

	jan1Date := time.Date(taxYear, 1, 1, 0, 0, 0, 0, time.UTC)
	yearEndDate := time.Date(taxYear, 12, 31, 0, 0, 0, 0, time.UTC)

	vpTxns := make([]analytics.VorabpauschaleTxn, 0, len(txns))
	for i := len(txns) - 1; i >= 0; i-- {
		txn := txns[i]
		if txn.Date.After(yearEndDate) || !txn.SecurityISIN.Valid {
			continue
		}
		isin := txn.SecurityISIN.String
		sec, ok := secMap[isin]
		if !ok || (sec.AssetClass != "etf" && sec.AssetClass != "fund") {
			continue
		}
		qty := numericToFloat(txn.Quantity)
		amtEUR := toEURAt(numericToFloat(txn.Amount), txn.Currency, txn.Date)
		feeEUR := toEURAt(numericToFloat(txn.Fee), txn.Currency, txn.Date)
		if txn.Type == "buy" || txn.Type == "savings_plan" || txn.Type == "transfer" {
			amtEUR += feeEUR
		}
		vpTxns = append(vpTxns, analytics.VorabpauschaleTxn{
			AccountID: txn.AccountID.String(),
			ISIN:      isin,
			Year:      txn.Date.Year(),
			Month:     int(txn.Date.Month()),
			Type:      txn.Type,
			Quantity:  qty,
			AmountEUR: amtEUR,
		})
	}

	positions := analytics.ReconstructVorabpauschaleYear(taxYear, vpTxns)
	if len(positions) == 0 {
		return 0
	}

	curUnitPrice := make(map[string]float64)
	if latestPrices, err := s.queries.ListLatestPrices(ctx); err == nil {
		for _, p := range latestPrices {
			if p.Close.Valid {
				curUnitPrice[p.SecurityISIN] = convertToEURScheduler(ctx, s.queries, numericToFloat(p.Close), p.Currency)
			}
		}
	}

	var vpInputs []analytics.VorabpauschaleInput
	for isin, pos := range positions {
		sec := secMap[isin]
		jan1Val := 0.0
		if pos.Jan1Qty > 1e-9 {
			if priceRow, err := s.queries.GetPriceAtDate(ctx, db.GetPriceAtDateParams{SecurityISIN: isin, Date: jan1Date}); err == nil && priceRow.Close.Valid {
				p := toEURAt(numericToFloat(priceRow.Close), priceRow.Currency, priceRow.Date)
				jan1Val = pos.Jan1Qty * p
			}
			if jan1Val <= 0 {
				jan1Val = pos.Jan1CostEUR
			}
		}
		yearEndVal := 0.0
		if priceRow, err := s.queries.GetPriceAtDate(ctx, db.GetPriceAtDateParams{SecurityISIN: isin, Date: yearEndDate}); err == nil && priceRow.Close.Valid {
			p := toEURAt(numericToFloat(priceRow.Close), priceRow.Currency, priceRow.Date)
			yearEndVal = pos.YearEndQty * p
		} else if curP := curUnitPrice[isin]; curP > 0 {
			yearEndVal = pos.YearEndQty * curP
		}
		vpInputs = append(vpInputs, analytics.VorabpauschaleInput{
			ISIN:          isin,
			Name:          sec.Name,
			Jan1Value:     jan1Val,
			YearEndValue:  yearEndVal,
			Distributions: pos.Distributions,
			MidYearBuys:   pos.MidYearBuys,
			TFSRate:       tfsMap[isin],
		})
	}

	entries := analytics.ComputeVorabpauschaleDetailed(taxYear, vpInputs)
	totalTax := 0.0
	for _, e := range entries {
		totalTax += e.TaxOnVP
	}
	return totalTax
}

// weeklyDataHealthDigest creates a notification summarizing data quality issues.
func (s *Scheduler) weeklyDataHealthDigest() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	now := time.Now().UTC()
	secs, _ := s.queries.ListSecurities(ctx)
	holdings, _ := s.queries.ListCurrentHoldings(ctx)

	seenISIN := make(map[string]bool)
	staleCount := 0
	for _, h := range holdings {
		if seenISIN[h.SecurityISIN] {
			continue
		}
		qty := numericToFloat(h.Quantity)
		if qty <= 0 {
			continue
		}
		seenISIN[h.SecurityISIN] = true
		priceRows, _ := s.queries.ListPriceHistory(ctx, h.SecurityISIN)
		if len(priceRows) == 0 {
			staleCount++
			continue
		}
		lastDate := priceRows[len(priceRows)-1].Date
		if now.Sub(lastDate).Hours() > 5*24 {
			staleCount++
		}
	}

	// Only check ETFs for JustETF composition metadata (stocks/crypto are not ETFs)
	missingMeta := 0
	for _, sec := range secs {
		if sec.AssetClass == "etf" && !sec.MetadataUpdatedAt.Valid {
			missingMeta++
		}
	}

	if staleCount == 0 && missingMeta == 0 {
		return // no issues, skip notification
	}

	msg := fmt.Sprintf("[Data Health] Weekly digest: %d stale prices, %d missing metadata", staleCount, missingMeta)
	val := pgtype.Numeric{}
	val.Scan(fmt.Sprintf("%d", staleCount+missingMeta))

	// Dedup: check if we already sent this week
	existing, _ := s.queries.ListNotifications(ctx, 20)
	for _, n := range existing {
		if strings.HasPrefix(n.Message, "[Data Health]") && now.Sub(n.TriggeredAt.UTC()).Hours() < 6*24 {
			return // already sent this week
		}
	}

	s.notifyAndDispatch(ctx, uuid.Nil, msg, val)
	log.Printf("data health digest: %d stale, %d missing metadata", staleCount, missingMeta)
}
