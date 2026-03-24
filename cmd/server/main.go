package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgtype"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/lnsp/wealth/data"
	"github.com/lnsp/wealth/frontend/dist"
	"github.com/lnsp/wealth/internal/config"
	"github.com/lnsp/wealth/internal/database"
	generated "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/handler"
	"github.com/lnsp/wealth/internal/market"
	"github.com/lnsp/wealth/internal/scheduler"
	"github.com/lnsp/wealth/migrations"
)

func main() {
	cfg := config.Load()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Database connection pool (pgx)
	pool, err := database.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()

	// Run migrations using database/sql (goose requirement)
	sqlDB, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("open sql connection for migrations: %v", err)
	}
	defer sqlDB.Close()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		log.Fatalf("set goose dialect: %v", err)
	}
	if err := goose.Up(sqlDB, "."); err != nil {
		log.Fatalf("run migrations: %v", err)
	}
	log.Println("migrations complete")

	// Initialize services
	queries := generated.New(pool)
	yahooClient := market.NewYahooClient()
	ecbClient := market.NewECBClient()

	// Load seed ticker map into database
	loadTickerMap(ctx, queries)

	// Start scheduler
	sched := scheduler.New(queries, yahooClient, ecbClient, cfg.BackupPath)
	if err := sched.Start(); err != nil {
		log.Fatalf("start scheduler: %v", err)
	}
	defer sched.Stop()

	// HTTP router
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))
	r.Use(middleware.Timeout(60 * time.Second))

	// API routes
	importH := handler.NewImportHandler(queries)
	txnH := handler.NewTransactionsHandler(queries)
	portfolioH := handler.NewPortfolioHandler(queries)
	analysisH := handler.NewAnalysisHandler(queries)
	settingsH := handler.NewSettingsHandler(queries)

	r.Route("/api", func(r chi.Router) {
		r.Post("/import", importH.HandleImport)

		r.Get("/transactions", txnH.HandleList)

		r.Get("/portfolio/holdings", portfolioH.HandleHoldings)
		r.Get("/portfolio/networth", portfolioH.HandleNetWorth)
		r.Get("/portfolio/accounts", portfolioH.HandleAccounts)

		r.Get("/analysis/sectors", analysisH.HandleSectors)
		r.Get("/analysis/countries", analysisH.HandleCountries)
		r.Get("/analysis/overlap", analysisH.HandleOverlap)
		r.Get("/analysis/etf/{isin}/holdings", analysisH.HandleETFHoldings)

		r.Post("/settings/accounts", settingsH.HandleCreateAccount)
		r.Put("/settings/accounts/{id}", settingsH.HandleUpdateAccount)
		r.Get("/settings/securities", settingsH.HandleListSecurities)
		r.Put("/settings/securities/{isin}/symbol", settingsH.HandleUpdateSecuritySymbol)
		r.Get("/settings/template", settingsH.HandleHoldingsTemplate)
	})

	// Serve React SPA
	spaFS, err := fs.Sub(dist.FS, ".")
	if err != nil {
		log.Fatalf("create SPA filesystem: %v", err)
	}
	fileServer := http.FileServer(http.FS(spaFS))
	r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
		// Try to serve static file; fall back to index.html for SPA routing
		path := req.URL.Path[1:]
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(spaFS, path); err != nil {
			req.URL.Path = "/index.html"
		}
		fileServer.ServeHTTP(w, req)
	})

	// Start server
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		log.Printf("server starting on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("shutdown: %v", err)
	}
}

func loadTickerMap(ctx context.Context, queries *generated.Queries) {
	var tickerMap map[string]string
	if err := json.Unmarshal(data.TickerMapJSON, &tickerMap); err != nil {
		log.Printf("WARNING: parse ticker map: %v", err)
		return
	}

	loaded := 0
	for isin, symbol := range tickerMap {
		// Only update symbol if security exists and has no symbol set
		sec, err := queries.GetSecurity(ctx, isin)
		if err != nil {
			continue // security doesn't exist yet
		}
		if sec.Symbol.Valid && sec.Symbol.String != "" {
			continue // already has a symbol
		}
		sym := pgtype.Text{String: symbol, Valid: true}
		if err := queries.UpdateSecuritySymbol(ctx, isin, sym); err != nil {
			log.Printf("WARNING: set ticker for %s: %v", isin, err)
			continue
		}
		loaded++
	}
	if loaded > 0 {
		log.Printf("loaded %d ticker mappings from seed file", loaded)
	}
}
