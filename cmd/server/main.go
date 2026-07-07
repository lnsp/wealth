package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgtype"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"golang.org/x/crypto/bcrypt"

	"github.com/lnsp/wealth/data"
	"github.com/lnsp/wealth/frontend"
	"github.com/lnsp/wealth/internal/auth"
	"github.com/lnsp/wealth/internal/config"
	"github.com/lnsp/wealth/internal/database"
	generated "github.com/lnsp/wealth/internal/database/generated"
	"github.com/lnsp/wealth/internal/handler"
	"github.com/lnsp/wealth/internal/market"
	"github.com/lnsp/wealth/internal/scheduler"
	"github.com/lnsp/wealth/migrations"
)

// Set via -ldflags at build time.
var (
	buildCommit = "dev"
	buildTime   = "unknown"
)

func main() {
	// Set up structured logging — redirect standard log through slog
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	log.SetFlags(0) // remove default timestamp (slog adds its own)
	log.SetOutput(slogWriter{logger})

	cfg := config.Load()

	// Security warnings
	if cfg.SessionSecret == "change-me" {
		log.Println("SECURITY WARNING: SESSION_SECRET is set to default value. Set a random 32+ byte secret in production.")
	} else if len(cfg.SessionSecret) < 32 {
		log.Printf("SECURITY WARNING: SESSION_SECRET is only %d bytes. Use at least 32 bytes for production.", len(cfg.SessionSecret))
	}
	if strings.Contains(cfg.DatabaseURL, "sslmode=disable") {
		log.Println("SECURITY WARNING: Database connection uses sslmode=disable. Use sslmode=require in production.")
	}

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

	// Ensure default admin user exists and assign orphaned data
	ensureDefaultUser(ctx, queries, cfg.AdminPassword)

	// Start scheduler
	justETFClient := market.NewJustETFClient()
	sched := scheduler.New(queries, yahooClient, ecbClient, justETFClient, cfg.BackupPath, cfg.BackupAgeRecipient)
	if err := sched.Start(); err != nil {
		log.Fatalf("start scheduler: %v", err)
	}
	defer sched.Stop()

	// HTTP router
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.GetHead)
	r.Use(middleware.Compress(5))
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(securityHeaders)

	// Rate limiting (configurable via RATE_LIMIT_API, RATE_LIMIT_UPLOAD, RATE_LIMIT_REPORT)
	apiLimiter := handler.NewRateLimiter(cfg.RateLimitAPI, time.Minute)
	uploadLimiter := handler.NewRateLimiter(cfg.RateLimitUpload, time.Hour)
	reportLimiter := handler.NewRateLimiter(cfg.RateLimitReport, time.Hour)

	// Authentication
	authInstance := auth.New(cfg.AdminPassword, cfg.SessionSecret)
	if authInstance != nil {
		log.Println("authentication enabled (ADMIN_PASSWORD is set)")
	} else {
		log.Println("WARNING: authentication disabled (ADMIN_PASSWORD not set)")
	}

	// API routes
	var tickerMap map[string]string
	if err := json.Unmarshal(data.TickerMapJSON, &tickerMap); err != nil {
		log.Printf("WARNING: parse ticker map: %v", err)
		tickerMap = make(map[string]string)
	}
	importH := handler.NewImportHandler(queries, sched, tickerMap)
	txnH := handler.NewTransactionsHandler(queries)
	cashflowH := handler.NewCashflowHandler(queries)
	portfolioH := handler.NewPortfolioHandler(queries)
	analysisH := handler.NewAnalysisHandler(queries)
	settingsH := handler.NewSettingsHandler(queries)
	alertsH := handler.NewAlertsHandler(queries)
	reportsH := handler.NewReportsHandler(queries)
	usersH := handler.NewUsersHandler(queries, authInstance, cfg.SessionSecret)

	// WebAuthn (passkey) handler — derive RP ID and origin from BASE_URL
	baseURL, err := url.Parse(cfg.BaseURL)
	if err != nil {
		log.Fatalf("invalid BASE_URL: %v", err)
	}
	webauthnH, webauthnErr := handler.NewWebAuthnHandler(queries, authInstance, baseURL.Hostname(), cfg.BaseURL)
	if webauthnErr != nil {
		slog.Warn("WebAuthn disabled", "error", webauthnErr)
	}

	// Health check for Docker/monitoring
	r.Get("/health", func(w http.ResponseWriter, req *http.Request) {
		if err := pool.Ping(req.Context()); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"status": "down", "error": "database unreachable"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	r.Route("/api", func(r chi.Router) {
		// Auth endpoints are always accessible (outside auth middleware)
		if authInstance != nil {
			authH := handler.NewAuthHandler(authInstance, queries, cfg.SessionSecret)
			r.Post("/auth/login", authH.HandleLogin)
			r.Post("/auth/logout", authH.HandleLogout)
			r.Get("/auth/status", authH.HandleStatus)
			// WebAuthn login must be outside auth middleware (user isn't authenticated yet)
			if webauthnH != nil {
				r.Post("/auth/webauthn/login/begin", webauthnH.HandleBeginLogin)
				r.Post("/auth/webauthn/login/finish", webauthnH.HandleFinishLogin)
			}
		} else {
			// Auth disabled: return {authenticated: false, required: false}
			r.Get("/auth/status", func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"authenticated": false, "required": false})
			})
		}

		// Protected API routes
		r.Group(func(r chi.Router) {
			if authInstance != nil {
				r.Use(handler.AuthMiddleware(authInstance))
			}
			r.Use(handler.RateLimitMiddleware(apiLimiter))

			// Import with stricter rate limit
			r.With(handler.RateLimitMiddleware(uploadLimiter)).Post("/import", importH.HandleImport)

			r.Get("/transactions", txnH.HandleList)
			r.Patch("/transactions/{id}/category", cashflowH.HandleUpdateCategory)
			r.Get("/cashflow", cashflowH.HandleCashflow)

			r.Get("/portfolio/holdings", portfolioH.HandleHoldings)
			r.Get("/portfolio/networth", portfolioH.HandleNetWorth)
			r.Get("/portfolio/networth/intraday", portfolioH.HandleNetWorthIntraday)
			r.Get("/portfolio/accounts", portfolioH.HandleAccounts)
			r.Get("/portfolio/performance", portfolioH.HandlePerformance)
			r.Get("/portfolio/performance-history", portfolioH.HandlePerformanceHistory)
			r.Get("/portfolio/dividends", portfolioH.HandleDividends)
			r.Get("/portfolio/allocation", portfolioH.HandleTargetAllocation)
			r.Put("/portfolio/allocation", portfolioH.HandleSetTargetAllocation)
			r.Get("/portfolio/goals", portfolioH.HandleGoalsProgress)
			r.Get("/portfolio/rebalance", portfolioH.HandleRebalance)
			r.Get("/portfolio/projection", portfolioH.HandleProjection)
			r.Get("/portfolio/savings-rate", portfolioH.HandleSavingsRate)
			r.Get("/portfolio/attribution", portfolioH.HandleAttribution)
			r.Get("/portfolio/security/{isin}", portfolioH.HandleSecurityDetail)
			r.Get("/portfolio/switch-compare", portfolioH.HandleSwitchCompare)
			r.Get("/portfolio/savings-plans", portfolioH.HandleSavingsPlans)
			r.Get("/portfolio/next-actions", portfolioH.HandleNextActions)
			r.Post("/portfolio/life-events", portfolioH.HandleLifeEvents)
			r.Get("/portfolio/sparplan-streak", portfolioH.HandleSparplanStreak)
			r.Get("/portfolio/wealth-waterfall", portfolioH.HandleWealthWaterfall)
			r.Get("/portfolio/time-machine", portfolioH.HandleTimeMachine)
			r.Post("/portfolio/pension-gap", portfolioH.HandlePensionGap)
			r.Get("/portfolio/unvested", portfolioH.HandleUnvested)

			r.Get("/analysis/summary", analysisH.HandleAllocationSummary)
			r.Get("/analysis/sectors", analysisH.HandleSectors)
			r.Get("/analysis/countries", analysisH.HandleCountries)
			r.Get("/analysis/overlap", analysisH.HandleOverlap)
			r.Get("/analysis/alerts", analysisH.HandleAlerts)
			r.Get("/analysis/top-holdings", analysisH.HandleTopHoldings)
			r.Get("/analysis/treemap", analysisH.HandleTreemap)
			r.Get("/analysis/sector-history", analysisH.HandleSectorHistory)
			r.Get("/analysis/etf/{isin}/holdings", analysisH.HandleETFHoldings)
			r.Get("/analysis/risk", analysisH.HandleRisk)
			r.Get("/analysis/currency", analysisH.HandleCurrency)
			r.Get("/analysis/tax", analysisH.HandleTax)
			r.Get("/analysis/costs", analysisH.HandleCosts)
			r.Get("/analysis/correlation", analysisH.HandleCorrelation)
			r.Get("/analysis/export-tax", analysisH.HandleExportTaxReport)
			r.Get("/analysis/fx-history", analysisH.HandleFXHistory)
			r.Get("/analysis/allocation-history", analysisH.HandleAllocationHistory)
			r.Get("/analysis/cash-flow", analysisH.HandleCashFlow)
			r.Get("/analysis/inflation", analysisH.HandleInflation)
			r.Get("/analysis/benchmark-comparison", analysisH.HandleBenchmarkComparison)
			r.Get("/analysis/health-score", analysisH.HandleHealthScore)
			r.Get("/analysis/alternatives", analysisH.HandleAlternatives)
			r.Get("/analysis/spending", analysisH.HandleSpending)
			r.Get("/analysis/tax-calendar", analysisH.HandleTaxCalendar)
			r.Get("/analysis/anlage-kap", analysisH.HandleAnlageKAP)
			r.Get("/analysis/export-datev", analysisH.HandleExportDATEV)
			r.Get("/analysis/volatility-context", analysisH.HandleVolatilityContext)
			r.Get("/analysis/opportunity-cost", analysisH.HandleOpportunityCost)
			r.Get("/analysis/data-quality", analysisH.HandleDataQuality)
			r.Get("/analysis/journal", analysisH.HandleJournalList)
			r.Post("/analysis/journal", analysisH.HandleJournalCreate)
			r.Put("/analysis/journal/{id}", analysisH.HandleJournalOutcome)
			r.Get("/analysis/tax-lots", analysisH.HandleTaxLots)
			r.Get("/analysis/loss-pots", analysisH.HandleLossPots)
			r.Get("/analysis/fsa-status", analysisH.HandleFSAStatus)
			r.Post("/analysis/sell-simulator", analysisH.HandleSellSimulator)
			r.Get("/analysis/crisis-stress-test", analysisH.HandleCrisisStressTest)

			r.Get("/settings/accounts", settingsH.HandleListAllAccounts)
			r.Post("/settings/accounts", settingsH.HandleCreateAccount)
			r.Put("/settings/accounts/{id}", settingsH.HandleUpdateAccount)
			r.Delete("/settings/accounts/{id}", settingsH.HandleDeleteAccount)
			r.Get("/settings/channels", settingsH.HandleListChannels)
			r.Post("/settings/channels", settingsH.HandleCreateChannel)
			r.Delete("/settings/channels/{id}", settingsH.HandleDeleteChannel)
			r.Get("/settings/securities", settingsH.HandleListSecurities)
			r.Put("/settings/securities/{isin}/symbol", settingsH.HandleUpdateSecuritySymbol)
			r.Get("/settings/template", settingsH.HandleHoldingsTemplate)
			r.Get("/settings/export-transactions", settingsH.HandleExportTransactions)
			r.Get("/settings/goals", settingsH.HandleListGoals)
			r.Post("/settings/goals", settingsH.HandleCreateGoal)
			r.Delete("/settings/goals/{id}", settingsH.HandleDeleteGoal)

			r.Get("/settings/alerts", alertsH.HandleListAlerts)
			r.Post("/settings/alerts", alertsH.HandleCreateAlert)
			r.Delete("/settings/alerts/{id}", alertsH.HandleDeleteAlert)
			r.Put("/settings/alerts/{id}/toggle", alertsH.HandleToggleAlert)
			r.Get("/notifications", alertsH.HandleListNotifications)
			r.Post("/notifications/read", alertsH.HandleMarkRead)

			r.Get("/settings/reports", reportsH.HandleListReports)
			r.Get("/settings/reports/{id}", reportsH.HandleGetReport)
			r.With(handler.RateLimitMiddleware(reportLimiter)).Post("/settings/reports", reportsH.HandleGenerateReport)
			r.Get("/settings/reports/{id}/pdf", reportsH.HandleDownloadPDF)

			r.Get("/settings/users", usersH.HandleListUsers)
			r.Post("/settings/users", usersH.HandleCreateUser)
			r.Delete("/settings/users/{id}", usersH.HandleDeleteUser)
			r.Put("/settings/users/{id}/toggle", usersH.HandleToggleUser)
			r.Post("/settings/users/{id}/totp/setup", usersH.HandleSetupTOTP)

			// WebAuthn (requires auth; login endpoints are registered above outside auth middleware)
			if webauthnH != nil {
				r.Post("/auth/webauthn/register/begin", webauthnH.HandleBeginRegistration)
				r.Post("/auth/webauthn/register/finish", webauthnH.HandleFinishRegistration)
				r.Get("/auth/webauthn/passkeys", webauthnH.HandleListPasskeys)
				r.Delete("/auth/webauthn/passkeys/{id}", webauthnH.HandleDeletePasskey)
			}
			r.Post("/settings/users/{id}/totp/verify", usersH.HandleVerifyTOTP)
			r.Delete("/settings/users/{id}/totp", usersH.HandleDisableTOTP)
			r.Get("/import-history", func(w http.ResponseWriter, req *http.Request) {
				history, err := queries.ListImportHistory(req.Context(), 50)
				if err != nil {
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]any{"history": []any{}})
					return
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{"history": history})
			})
			r.Get("/settings/scheduler-status", func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]any{
					"jobs":     sched.GetJobStatuses(),
					"backfill": sched.GetBackfillStatuses(req.Context()),
				})
			})

			// Manual scheduler triggers
			r.Post("/settings/refresh-prices", func(w http.ResponseWriter, req *http.Request) {
				sched.RunPriceUpdate()
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{"status": "started"})
			})
			r.Post("/settings/refresh-etf-metadata", func(w http.ResponseWriter, req *http.Request) {
				go sched.RunETFUpdate()
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{"status": "started"})
			})
			r.Post("/settings/refresh-historical-prices", func(w http.ResponseWriter, req *http.Request) {
				sched.RunHistoricalPricesBackfill()
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{"status": "started"})
			})
			r.Post("/settings/rebuild-networth", func(w http.ResponseWriter, req *http.Request) {
				go sched.BackfillNetWorthSnapshots()
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{"status": "started"})
			})
			r.Post("/settings/check-alerts", func(w http.ResponseWriter, req *http.Request) {
				sched.RunAlertCheck()
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{"status": "started"})
			})
			r.Get("/settings/build-info", func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{
					"commit":     buildCommit,
					"built_at":   buildTime,
				})
			})
		}) // end Group
	})

	// Serve React SPA
	spaFS, err := fs.Sub(frontend.FS, "dist")
	if err != nil {
		log.Fatalf("create SPA filesystem: %v", err)
	}
	// Read index.html once for SPA fallback (avoids FileServer redirect)
	indexHTML, err := fs.ReadFile(spaFS, "index.html")
	if err != nil {
		log.Fatalf("read index.html: %v", err)
	}
	fileServer := http.FileServer(http.FS(spaFS))
	r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
		// Try to serve static file; fall back to index.html for SPA routing
		path := req.URL.Path[1:]
		if path == "" || path == "index.html" {
			// Always serve index.html with no-cache
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache, must-revalidate")
			w.Write(indexHTML)
			return
		}
		if _, err := fs.Stat(spaFS, path); err != nil {
			// Serve index.html directly to preserve the URL for React Router
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache, must-revalidate")
			w.Write(indexHTML)
			return
		}

		// Set cache headers based on file type
		if strings.HasPrefix(path, "assets/") {
			// Vite hashed assets: cache forever (hash changes on content change)
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else if path == "sw.js" {
			// Service worker: no-cache to ensure updates are picked up immediately
			w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		} else if strings.HasPrefix(path, "fonts/") || strings.HasPrefix(path, "icons/") {
			// Static assets: cache for 30 days
			w.Header().Set("Cache-Control", "public, max-age=2592000")
		} else if path == "manifest.json" {
			w.Header().Set("Cache-Control", "public, max-age=3600")
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
		if err := queries.UpdateSecuritySymbol(ctx, generated.UpdateSecuritySymbolParams{ISIN: isin, Symbol: sym}); err != nil {
			log.Printf("WARNING: set ticker for %s: %v", isin, err)
			continue
		}
		loaded++
	}
	if loaded > 0 {
		log.Printf("loaded %d ticker mappings from seed file", loaded)
	}
}

// securityHeaders adds standard security headers to all responses.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		// CSP: allow self, inline styles (Tailwind), ECharts canvas, and data: URIs for fonts
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'")

		// CSRF: verify Origin or Referer header on state-changing requests
		if r.Method == "POST" || r.Method == "PUT" || r.Method == "DELETE" || r.Method == "PATCH" {
			origin := r.Header.Get("Origin")
			referer := r.Header.Get("Referer")
			host := r.Host

			if origin != "" {
				if !strings.Contains(origin, host) {
					slog.Warn("CSRF: origin mismatch", "origin", origin, "host", host)
					http.Error(w, `{"error":"origin mismatch"}`, http.StatusForbidden)
					return
				}
			} else if referer != "" {
				if !strings.Contains(referer, host) {
					slog.Warn("CSRF: referer mismatch", "referer", referer, "host", host)
					http.Error(w, `{"error":"origin mismatch"}`, http.StatusForbidden)
					return
				}
			} else {
				// Neither Origin nor Referer present — reject to prevent CSRF
				slog.Warn("CSRF: missing origin and referer headers", "method", r.Method, "path", r.URL.Path)
				http.Error(w, `{"error":"origin required"}`, http.StatusForbidden)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// ensureDefaultUser creates admin user on first run and assigns orphaned data.
func ensureDefaultUser(ctx context.Context, q *generated.Queries, adminPassword string) {
	count, err := q.CountUsers(ctx)
	if err != nil {
		log.Printf("WARNING: count users: %v", err)
		return
	}
	if count > 0 {
		return // users already exist
	}

	// Create default admin user
	password := adminPassword
	if password == "" {
		log.Fatal("ADMIN_PASSWORD must be set. Refusing to create admin user with default password.")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("WARNING: hash admin password: %v", err)
		return
	}

	user, err := q.CreateUser(ctx, generated.CreateUserParams{
		Username: "admin", PasswordHash: string(hash), Role: "admin",
	})
	if err != nil {
		log.Printf("WARNING: create default admin: %v", err)
		return
	}
	log.Printf("created default admin user: %s", user.ID)

	if err := q.AssignOrphanedDataToUser(ctx, user.ID); err != nil {
		log.Printf("WARNING: assign orphaned data: %v", err)
	} else {
		log.Println("assigned existing data to admin user")
	}
}

// slogWriter adapts slog.Logger to io.Writer for redirecting standard log output.
type slogWriter struct{ logger *slog.Logger }

func (w slogWriter) Write(p []byte) (int, error) {
	msg := strings.TrimRight(string(p), "\n")
	w.logger.Info(msg)
	return len(p), nil
}
