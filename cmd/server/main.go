package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/sociolytik/odoo-clone/internal/activities"
	"github.com/sociolytik/odoo-clone/internal/ai"
	"github.com/sociolytik/odoo-clone/internal/attachments"
	"github.com/sociolytik/odoo-clone/internal/auth"
	"github.com/sociolytik/odoo-clone/internal/calendar"
	"github.com/sociolytik/odoo-clone/internal/chat"
	"github.com/sociolytik/odoo-clone/internal/config"
	"github.com/sociolytik/odoo-clone/internal/crm"
	"github.com/sociolytik/odoo-clone/internal/dashboard"
	"github.com/sociolytik/odoo-clone/internal/db"
	"github.com/sociolytik/odoo-clone/internal/helpdesk"
	"github.com/sociolytik/odoo-clone/internal/invoicing"
	"github.com/sociolytik/odoo-clone/internal/notifications"
	"github.com/sociolytik/odoo-clone/internal/portal"
	"github.com/sociolytik/odoo-clone/internal/projects"
	"github.com/sociolytik/odoo-clone/internal/search"
	"github.com/sociolytik/odoo-clone/internal/settings"
	"github.com/sociolytik/odoo-clone/internal/social"
	"github.com/sociolytik/odoo-clone/internal/timeoff"
	"github.com/sociolytik/odoo-clone/internal/timesheets"
	"github.com/sociolytik/odoo-clone/internal/web"
	"github.com/sociolytik/odoo-clone/internal/wiki"
)

const attachmentsDir = "data/attachments"
const socialMediaDir = "data/social"

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	log.Println("migrations applied")

	renderer := web.NewRenderer("web/templates")

	authRepo := auth.NewRepo(pool)
	authMW := auth.NewMiddleware(authRepo)
	authHandlers := auth.NewHandlers(authRepo, renderer, cfg.CookieSecure, time.Duration(cfg.SessionTTLDay)*24*time.Hour)

	if err := authRepo.EnsureAdminExists(ctx); err != nil {
		log.Fatalf("ensure admin exists: %v", err)
	}
	// Optional convenience for deployment: promotes an already-registered
	// user to admin without needing manual SQL. Never fatal — it's a
	// nice-to-have, not a startup invariant like EnsureAdminExists.
	if err := authRepo.PromoteAdminByEmail(ctx, cfg.AdminEmail); err != nil {
		log.Printf("promote admin by email: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	dashboardRepo := dashboard.NewRepo(pool)
	dashboardHandlers := dashboard.NewHandlers(dashboardRepo, renderer)
	dashboardHandlers.MountRoutes(mux)

	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))
	mux.HandleFunc("GET /manifest.json", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "web/static/manifest.json")
	})
	// Served at the root (not /static/sw.js) so its default scope covers the
	// whole app — a service worker's registration scope is capped at its own
	// directory unless served from root or given a Service-Worker-Allowed
	// header, and it needs to control every page, not just /static/.
	mux.HandleFunc("GET /sw.js", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "web/static/sw.js")
	})

	authHandlers.MountRoutes(mux, authMW)

	if err := os.MkdirAll(attachmentsDir, 0o755); err != nil {
		log.Fatalf("create attachments dir: %v", err)
	}
	if err := os.MkdirAll(socialMediaDir, 0o755); err != nil {
		log.Fatalf("create social media dir: %v", err)
	}
	attachmentsRepo := attachments.NewRepo(pool)

	settingsRepo := settings.NewRepo(pool)
	aiRepo := ai.NewRepo(pool)
	aiHandlers := ai.NewHandlers(aiRepo)
	aiHandlers.MountRoutes(mux, authMW)
	settingsHandlers := settings.NewHandlers(settingsRepo, authRepo, aiRepo, renderer, cfg.BaseURL)
	settingsHandlers.MountRoutes(mux, authMW)

	notificationsRepo := notifications.NewRepo(pool, authRepo, settingsRepo, cfg.BaseURL)
	notificationsHandlers := notifications.NewHandlers(notificationsRepo, renderer)
	notificationsHandlers.MountRoutes(mux, authMW)

	activitiesRepo := activities.NewRepo(pool)

	projectsRepo := projects.NewRepo(pool)
	projectsHandlers := projects.NewHandlers(projectsRepo, authRepo, attachmentsRepo, notificationsRepo, activitiesRepo, renderer)
	projectsHandlers.MountRoutes(mux, authMW)

	attachmentsHandlers := attachments.NewHandlers(attachmentsRepo, projectsRepo, renderer, attachmentsDir)
	attachmentsHandlers.MountRoutes(mux, authMW)

	activitiesHandlers := activities.NewHandlers(activitiesRepo, projectsRepo, notificationsRepo, renderer)
	activitiesHandlers.MountRoutes(mux, authMW)

	timesheetsRepo := timesheets.NewRepo(pool)
	timesheetsHandlers := timesheets.NewHandlers(timesheetsRepo, projectsRepo, notificationsRepo, renderer)
	timesheetsHandlers.MountRoutes(mux, authMW)

	timeoffRepo := timeoff.NewRepo(pool)
	timeoffHandlers := timeoff.NewHandlers(timeoffRepo, notificationsRepo, renderer)
	timeoffHandlers.MountRoutes(mux, authMW)

	invoicingRepo := invoicing.NewRepo(pool)
	invoicingHandlers := invoicing.NewHandlers(invoicingRepo, renderer)
	invoicingHandlers.MountRoutes(mux, authMW)

	wikiRepo := wiki.NewRepo(pool)
	wikiHandlers := wiki.NewHandlers(wikiRepo, projectsRepo, renderer)
	wikiHandlers.MountRoutes(mux, authMW)

	helpdeskRepo := helpdesk.NewRepo(pool)
	helpdeskHandlers := helpdesk.NewHandlers(helpdeskRepo, authRepo, notificationsRepo, renderer)
	helpdeskHandlers.MountRoutes(mux, authMW)

	calendarHandlers := calendar.NewHandlers(projectsRepo, timesheetsRepo, renderer)
	calendarHandlers.MountRoutes(mux, authMW)

	crmRepo := crm.NewRepo(pool)
	crmHandlers := crm.NewHandlers(crmRepo, authRepo, notificationsRepo, renderer)
	crmHandlers.MountRoutes(mux, authMW)

	searchRepo := search.NewRepo(pool)
	searchHandlers := search.NewHandlers(searchRepo, renderer)
	searchHandlers.MountRoutes(mux, authMW)

	portalRepo := portal.NewRepo(pool)
	portalMW := portal.NewMiddleware(portalRepo)
	portalHandlers := portal.NewHandlers(portalRepo, settingsRepo, renderer, cfg.BaseURL, cfg.CookieSecure)
	portalHandlers.MountRoutes(mux, authMW, portalMW)

	socialRepo := social.NewRepo(pool, cfg.BaseURL)
	socialHandlers := social.NewHandlers(socialRepo, renderer, cfg.BaseURL, socialMediaDir)
	socialHandlers.MountRoutes(mux, authMW)

	chatHub := chat.NewHub()
	go chatHub.Run()
	chatRepo := chat.NewRepo(pool)
	chatHandlers := chat.NewHandlers(chatRepo, authRepo, renderer, chatHub, attachmentsDir)
	chatHandlers.MountRoutes(mux, authMW)

	handler := authMW.LoadUser(mux)

	srv := &http.Server{
		Addr:         cfg.Addr,
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := authRepo.DeleteExpiredSessions(ctx); err != nil {
					log.Printf("cleanup expired sessions: %v", err)
				}
				if err := portalRepo.DeleteExpiredSessions(ctx); err != nil {
					log.Printf("cleanup expired portal sessions: %v", err)
				}
				if err := portalRepo.DeleteExpiredMagicLinks(ctx); err != nil {
					log.Printf("cleanup expired portal magic links: %v", err)
				}
				if err := socialRepo.DeleteExpiredOAuthStates(ctx); err != nil {
					log.Printf("cleanup expired social oauth states: %v", err)
				}
			}
		}
	}()

	// Due-date reminders: date-based (end_date = CURRENT_DATE) and gated by
	// a sent-once flag, so a 15-minute cadence just controls how promptly a
	// reminder fires after midnight — it's self-healing across restarts,
	// not a precise scheduler.
	go func() {
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				projectsHandlers.NotifyDueTasks(ctx)
				activitiesHandlers.NotifyDueActivities(ctx)
			}
		}
	}()

	// Scheduled social posts: a post created for "now" publishes
	// synchronously inside the request instead of waiting for this — this
	// ticker only ever picks up posts whose scheduled_at has arrived.
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				socialHandlers.PublishDuePosts(ctx)
			}
		}
	}()

	go func() {
		log.Printf("listening on %s", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
