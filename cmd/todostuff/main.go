package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/andyjessop/todostuff/internal/auth"
	"github.com/andyjessop/todostuff/internal/database"
	"github.com/andyjessop/todostuff/internal/handlers"
	appmw "github.com/andyjessop/todostuff/internal/middleware"
	"github.com/andyjessop/todostuff/internal/notifications"
	"github.com/andyjessop/todostuff/internal/render"
	"github.com/andyjessop/todostuff/internal/services"
	"github.com/andyjessop/todostuff/migrations"
	"github.com/andyjessop/todostuff/web"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	dbPath := envOr("DATABASE_PATH", "./data/todostuff.db")
	port := envOr("PORT", "8080")
	cookieSecure := envBool("COOKIE_SECURE", false)
	pushoverToken := os.Getenv("PUSHOVER_APP_TOKEN")

	db, err := database.Open(dbPath)
	if err != nil {
		logger.Error("open database", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := database.Migrate(db, migrations.FS); err != nil {
		logger.Error("migrate database", "err", err)
		os.Exit(1)
	}
	logger.Info("database ready", "path", dbPath)

	authSvc := auth.NewService(db, cookieSecure)
	tasksSvc := services.NewTasks(db)
	projectsSvc := services.NewProjects(db)
	pushover := notifications.NewPushover(pushoverToken)
	if pushover.Enabled() {
		logger.Info("pushover enabled")
	} else {
		logger.Info("pushover disabled (PUSHOVER_APP_TOKEN unset)")
	}

	renderer, err := render.New(web.TemplateFS)
	if err != nil {
		logger.Error("init renderer", "err", err)
		os.Exit(1)
	}

	h := handlers.New(authSvc, tasksSvc, projectsSvc, pushover, renderer)

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(appmw.AllowHead)

	// Public
	r.Get("/health", handlers.Health)
	r.Get("/", h.Root)
	r.Get("/setup", h.SetupPage)
	r.Post("/setup", h.SetupSubmit)
	r.Get("/login", h.LoginPage)
	r.Post("/login", h.LoginSubmit)

	// Static
	staticFS := http.FileServer(http.Dir("./web/static"))
	r.Handle("/static/*", http.StripPrefix("/static/", staticFS))

	// Protected
	r.Group(func(pr chi.Router) {
		pr.Use(appmw.RequireAuth(authSvc))
		pr.Post("/logout", h.Logout)
		pr.Get("/today", h.Today)
		pr.Get("/inbox", h.Inbox)
		pr.Get("/upcoming", h.Upcoming)
		pr.Get("/anytime", h.Anytime)
		pr.Get("/logbook", h.Logbook)

		pr.Post("/tasks", h.TaskCreate)
		pr.Post("/tasks/reorder", h.TaskReorder)
		pr.Get("/tasks/{id}", h.TaskDetail)
		pr.Put("/tasks/{id}", h.TaskUpdate)
		pr.Delete("/tasks/{id}", h.TaskDelete)
		pr.Post("/tasks/{id}/complete", h.TaskComplete)
		pr.Post("/tasks/{id}/uncomplete", h.TaskUncomplete)

		pr.Get("/api/reminders/due", h.RemindersDue)

		pr.Post("/projects", h.ProjectCreate)
		pr.Post("/projects/reorder", h.ProjectReorder)
		pr.Get("/projects/{id}", h.ProjectView)
		pr.Put("/projects/{id}", h.ProjectUpdate)
		pr.Delete("/projects/{id}", h.ProjectDelete)
		pr.Post("/projects/{id}/move", h.ProjectMove)

		// Profile. POST mirrors PUT so plain HTML forms (which can't issue PUT)
		// hit the same handler — keeps the curl-driven verification in the
		// master plan and the browser form both working from one route.
		pr.Get("/profile", h.ProfilePage)
		pr.Put("/profile", h.ProfileUpdate)
		pr.Post("/profile", h.ProfileUpdate)
		pr.Post("/profile/password", h.ProfilePassword)
		pr.Post("/profile/pushover/test", h.ProfilePushoverTest)

		// Admin (gated by RequireAdmin on top of RequireAuth). Same PUT/POST
		// dual-mount trick as /profile so curl + browser forms both work.
		pr.Group(func(ar chi.Router) {
			ar.Use(appmw.RequireAdmin)
			ar.Get("/admin/users", h.AdminUsersPage)
			ar.Post("/admin/users", h.AdminUserCreate)
			ar.Get("/admin/users/{id}", h.AdminUserEditPage)
			ar.Put("/admin/users/{id}", h.AdminUserUpdate)
			ar.Post("/admin/users/{id}", h.AdminUserUpdate)
			ar.Delete("/admin/users/{id}", h.AdminUserDelete)
			ar.Post("/admin/users/{id}/reset-password", h.AdminUserResetPassword)
		})
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Background dispatcher: fires Pushover for any due reminder belonging to
	// a user with pushover_enabled=1 + key set. Independent of the JS browser
	// poller — different *_sent_at column, so both channels fire once each.
	dispatcherCtx, dispatcherStop := context.WithCancel(context.Background())
	defer dispatcherStop()
	go runReminderDispatcher(dispatcherCtx, tasksSvc, pushover, logger)

	go func() {
		logger.Info("server listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	logger.Info("shutting down")

	dispatcherStop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("shutdown error", "err", err)
	}
}

// runReminderDispatcher polls every 60s for due Pushover reminders and sends
// them. Skips entirely when Pushover isn't configured — saves the DB scan.
// On send failure we log but leave the row marked sent: retrying every minute
// against a misconfigured user key would just spam the API. The user gets a
// new chance the next time they edit the reminder (which clears the flag).
func runReminderDispatcher(ctx context.Context, tasks *services.Tasks, push *notifications.Pushover, logger *slog.Logger) {
	if !push.Enabled() {
		return
	}
	const tick = 60 * time.Second

	// Fire immediately on startup so a reminder that came due during downtime
	// doesn't have to wait a full minute.
	dispatchOnce(ctx, tasks, push, logger)

	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			dispatchOnce(ctx, tasks, push, logger)
		}
	}
}

func dispatchOnce(ctx context.Context, tasks *services.Tasks, push *notifications.Pushover, logger *slog.Logger) {
	due, err := tasks.PopDuePushoverReminders(ctx)
	if err != nil {
		logger.Error("pop due pushover reminders", "err", err)
		return
	}
	for _, r := range due {
		err := push.Send(ctx, notifications.SendParams{
			UserKey: r.PushoverUserKey,
			Title:   r.Title,
			Message: r.Notes,
		})
		if err != nil {
			logger.Warn("pushover send", "task_id", r.TaskID, "err", err)
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
