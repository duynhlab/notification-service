package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/duynhlab/notification-service/config"
	migrations "github.com/duynhlab/notification-service/db/migrations"
	database "github.com/duynhlab/notification-service/internal/core"
	"github.com/duynhlab/notification-service/internal/core/repository"
	grpcv1 "github.com/duynhlab/notification-service/internal/grpc/v1"
	logicv1 "github.com/duynhlab/notification-service/internal/logic/v1"
	webv1 "github.com/duynhlab/notification-service/internal/web/v1"
	"github.com/duynhlab/notification-service/middleware"
	"github.com/duynhlab/pkg/authmw"
	"github.com/duynhlab/pkg/grpcx"
	"github.com/duynhlab/pkg/logger/zapx"
	"github.com/duynhlab/pkg/migratex"
	"github.com/duynhlab/pkg/obsx"
	authv1 "github.com/duynhlab/pkg/proto/auth/v1"
	notificationv1 "github.com/duynhlab/pkg/proto/notification/v1"
)

func main() {
	cfg := config.Load()

	logger, err := zapx.New(os.Getenv("LOG_LEVEL"))
	if err != nil {
		panic("Failed to initialize logger: " + err.Error())
	}
	defer func() { _ = logger.Sync() }()

	// `<binary> migrate` runs embedded schema migrations (init container, against
	// the direct DB host) and exits; no args serves the app.
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		if err := migratex.Run(migrations.FS, "sql", cfg.Database.BuildDSN()); err != nil {
			logger.Fatal("Schema migration failed", zap.Error(err))
		}
		logger.Info("Schema migrations applied")
		return
	}

	if err := cfg.Validate(); err != nil {
		panic("Configuration validation failed: " + err.Error())
	}

	logger.Info("Service starting",
		zap.String("service", cfg.Service.Name),
		zap.String("version", cfg.Service.Version),
		zap.String("env", cfg.Service.Env),
		zap.String("port", cfg.Service.Port),
	)

	tp := initTracing(cfg, logger)

	// Bridge gRPC OTel RED metrics onto the existing Prometheus /metrics endpoint.
	// Must run before grpcx.NewServer so the otelgrpc handlers pick up the global
	// MeterProvider.
	if cfg.Metrics.Enabled {
		shutdownMetrics, err := obsx.SetupMetrics()
		if err != nil {
			logger.Warn("Failed to initialize metrics", zap.Error(err))
		} else {
			logger.Info("Metrics initialized (gRPC RED metrics on /metrics)")
			defer func() { _ = shutdownMetrics(context.Background()) }()
		}
	}

	// Initialize Pyroscope profiling via the shared obsx helper.
	if cfg.Profiling.Enabled {
		stopProfiling, err := obsx.SetupProfiling()
		if err != nil {
			logger.Warn("Failed to initialize profiling", zap.Error(err))
		} else {
			logger.Info("Profiling initialized", zap.String("endpoint", cfg.Profiling.Endpoint))
			defer func() { _ = stopProfiling(context.Background()) }()
		}
	} else {
		logger.Info("Profiling disabled (PROFILING_ENABLED=false)")
	}

	pool, err := database.Connect(context.Background())
	if err != nil {
		logger.Error("Failed to connect to database", zap.Error(err))
		return
	}
	defer pool.Close()
	logger.Info("Database connection pool established")

	// Dependency Injection
	repo := repository.NewNotificationRepository(pool)
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

	// Local JWT verification via JWKS; opaque tokens fall back to the gRPC GetMe path.
	verifier, err := authmw.NewVerifier(cfg.JWKSURL, cfg.JWTIssuer, cfg.JWTAudience)
	if err != nil {
		logger.Warn("Failed to initialize JWT verifier; falling back to gRPC validation only", zap.Error(err))
	}

	// Internal gRPC server (east-west). HTTP :8080 is unaffected.
	grpcSrv := startGRPC(cfg, logger, service)

	var isShuttingDown atomic.Bool
	srv := setupServer(cfg, logger, &isShuttingDown, handler, verifier, authClient, pool)
	runGracefulShutdown(cfg, srv, grpcSrv, tp, pool, logger, &isShuttingDown)
}

// startGRPC starts the internal gRPC server on cfg.GRPC.Port, serving
// NotificationService alongside the HTTP listener (dual-port). gRPC is the
// official east-west transport, so it always runs; it returns nil only if the
// listener can't bind. The server uses the shared grpcx bootstrap (OpenTelemetry,
// health, reflection).
func startGRPC(cfg *config.Config, logger *zap.Logger, svc *logicv1.NotificationService) *grpc.Server {
	lc := net.ListenConfig{}
	lis, err := lc.Listen(context.Background(), "tcp", ":"+cfg.GRPC.Port)
	if err != nil {
		logger.Error("Failed to listen for gRPC", zap.String("port", cfg.GRPC.Port), zap.Error(err))
		return nil
	}

	grpcSrv, _ := grpcx.NewServer()
	notificationv1.RegisterNotificationServiceServer(grpcSrv, grpcv1.NewServer(svc))

	go func() {
		logger.Info("Starting gRPC server", zap.String("port", cfg.GRPC.Port))
		if err := grpcSrv.Serve(lis); err != nil {
			logger.Error("gRPC server error", zap.Error(err))
		}
	}()

	return grpcSrv
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

func setupServer(
	cfg *config.Config,
	logger *zap.Logger,
	isShuttingDown *atomic.Bool,
	handler *webv1.Handler,
	verifier *authmw.Verifier,
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
	privateNotif.Use(authmw.MiddlewareJWT(verifier, authClient))
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
	grpcSrv *grpc.Server,
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

	if grpcSrv != nil {
		grpcSrv.GracefulStop()
		logger.Info("gRPC server shutdown complete")
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

	logger.Info("Graceful shutdown complete")
}
