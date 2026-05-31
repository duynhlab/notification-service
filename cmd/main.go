package main

import (
	"context"
	"errors"
	"net/http"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"github.com/duynhlab/notification-service/config"
	database "github.com/duynhlab/notification-service/internal/core"
	logicv1 "github.com/duynhlab/notification-service/internal/logic/v1"
	webv1 "github.com/duynhlab/notification-service/internal/web/v1"
	"github.com/duynhlab/notification-service/middleware"
	"github.com/duynhlab/pkg/authmw"
	"github.com/duynhlab/pkg/grpcx"
	authv1 "github.com/duynhlab/pkg/proto/auth/v1"
)

func main() {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		panic("Configuration validation failed: " + err.Error())
	}

	logger, err := middleware.NewLogger()
	if err != nil {
		panic("Failed to initialize logger: " + err.Error())
	}
	defer func() { _ = logger.Sync() }()

	logger.Info("Service starting",
		zap.String("service", cfg.Service.Name),
		zap.String("version", cfg.Service.Version),
		zap.String("env", cfg.Service.Env),
		zap.String("port", cfg.Service.Port),
	)

	tp := initTracing(cfg, logger)

	initProfiling(cfg, logger)

	pool, err := database.Connect(context.Background())
	if err != nil {
		logger.Error("Failed to connect to database", zap.Error(err))
		return
	}
	defer pool.Close()
	logger.Info("Database connection pool established")

	// Dependency Injection
	repo := database.NewNotificationRepository()
	service := logicv1.NewNotificationService(repo)
	handler := webv1.NewHandler(service)

	// Validate tokens against auth over gRPC (shared fail-closed authmw).
	authConn, err := grpcx.Dial(cfg.AuthGRPCAddr)
	if err != nil {
		logger.Error("Failed to dial auth gRPC", zap.String("addr", cfg.AuthGRPCAddr), zap.Error(err))
		return
	}
	defer func() { _ = authConn.Close() }()
	authClient := authv1.NewAuthServiceClient(authConn)
	logger.Info("Auth gRPC client initialized", zap.String("auth_grpc_addr", cfg.AuthGRPCAddr))

	var isShuttingDown atomic.Bool
	srv := setupServer(cfg, logger, &isShuttingDown, handler, authClient, pool)
	runGracefulShutdown(cfg, srv, tp, pool, logger, &isShuttingDown)
}

func initTracing(cfg *config.Config, logger *zap.Logger) interface{ Shutdown(context.Context) error } {
	if !cfg.Tracing.Enabled {
		logger.Info("Tracing disabled (TRACING_ENABLED=false)")
		return nil
	}
	tp, err := middleware.InitTracing(cfg)
	if err != nil {
		logger.Warn("Failed to initialize tracing", zap.Error(err))
		return nil
	}
	logger.Info("Tracing initialized",
		zap.String("endpoint", cfg.Tracing.Endpoint),
		zap.Float64("sample_rate", cfg.Tracing.SampleRate),
	)
	return tp
}

func initProfiling(cfg *config.Config, logger *zap.Logger) {
	if !cfg.Profiling.Enabled {
		logger.Info("Profiling disabled (PROFILING_ENABLED=false)")
		return
	}
	if err := middleware.InitProfiling(); err != nil {
		logger.Warn("Failed to initialize profiling", zap.Error(err))
		return
	}
	logger.Info("Profiling initialized", zap.String("endpoint", cfg.Profiling.Endpoint))
}

func setupServer(
	cfg *config.Config,
	logger *zap.Logger,
	isShuttingDown *atomic.Bool,
	handler *webv1.Handler,
	authClient authv1.AuthServiceClient,
	pool interface {
		Ping(context.Context) error
	},
) *http.Server {
	r := gin.Default()

	r.Use(middleware.TracingMiddleware())
	r.Use(middleware.LoggingMiddleware(logger))
	r.Use(middleware.PrometheusMiddleware())

	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})
	r.GET("/ready", func(c *gin.Context) {
		if isShuttingDown.Load() {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "shutting_down"})
			return
		}
		pingCtx, cancel := context.WithTimeout(c.Request.Context(), 1*time.Second)
		defer cancel()
		if err := pool.Ping(pingCtx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "db_unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Notification v1 routes — Variant A edge naming (see api-naming-convention.md)

	// Private: user-facing notification list/count/detail/mark-read (JWT required)
	privateNotif := r.Group("/notification/v1/private")
	privateNotif.Use(authmw.Middleware(authClient))
	{
		privateNotif.GET("/notifications", handler.ListNotifications)
		privateNotif.GET("/notifications/count", handler.GetUnreadCount)
		privateNotif.GET("/notifications/:id", handler.GetNotification)
		privateNotif.PATCH("/notifications/:id", handler.MarkAsRead)
	}

	// Internal: service-to-service (e.g. order-service triggers email). Not on gateway.
	internalNotif := r.Group("/notification/v1/internal")
	{
		internalNotif.POST("/notify/email", handler.SendEmail)
		internalNotif.POST("/notify/sms", handler.SendSMS)
	}

	return &http.Server{
		Addr:              ":" + cfg.Service.Port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

func runGracefulShutdown(
	cfg *config.Config,
	srv *http.Server,
	tp interface{ Shutdown(context.Context) error },
	pool interface{ Close() },
	logger *zap.Logger,
	isShuttingDown *atomic.Bool,
) {
	go func() {
		logger.Info("Starting notification service", zap.String("port", cfg.Service.Port))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Failed to start server", zap.Error(err))
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	<-ctx.Done()
	logger.Info("Shutdown signal received")

	isShuttingDown.Store(true)
	drainDelay := cfg.GetReadinessDrainDelayDuration()
	if drainDelay > 0 {
		logger.Info("Readiness drain delay started", zap.Duration("delay", drainDelay))
		time.Sleep(drainDelay)
	}

	shutdownTimeout := cfg.GetShutdownTimeoutDuration()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	logger.Info("Shutting down server...", zap.Duration("timeout", shutdownTimeout))

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", zap.Error(err))
	} else {
		logger.Info("HTTP server shutdown complete")
	}

	pool.Close()
	logger.Info("Database pool closed")

	if tp != nil {
		if err := tp.Shutdown(shutdownCtx); err != nil {
			logger.Error("Tracer shutdown error", zap.Error(err))
		} else {
			logger.Info("Tracer shutdown complete")
		}
	}

	middleware.StopProfiling()
	logger.Info("Graceful shutdown complete")
}
