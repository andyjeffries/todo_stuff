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
	"github.com/andyjessop/todostuff/internal/render"
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

	renderer, err := render.New(web.TemplateFS)
	if err != nil {
		logger.Error("init renderer", "err", err)
		os.Exit(1)
	}

	h := handlers.New(authSvc, renderer)

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
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("shutdown error", "err", err)
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
