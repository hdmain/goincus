package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hdmain/goincus/internal/api"
	"github.com/hdmain/goincus/internal/config"
	"github.com/hdmain/goincus/internal/db"
	"github.com/hdmain/goincus/internal/incusclient"
	"github.com/hdmain/goincus/internal/ports"
	"github.com/hdmain/goincus/internal/redisstore"
	"github.com/hdmain/goincus/internal/service"
	"github.com/hdmain/goincus/internal/setup"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "init":
		os.Exit(runInit(os.Args[2:]))
	case "serve", "run":
		os.Exit(runServe(os.Args[2:]))
	case "version", "-version", "--version":
		fmt.Println(version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(2)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `goincus — Incus NAT VPS provisioning API

Usage:
  goincus init [--force] [--skip-install] [--config PATH]
  goincus serve [-config PATH] [-migrations DIR]
  goincus version

Commands:
  init    Install PostgreSQL, Redis, and Incus; generate config, passwords, and API keys
  serve   Start the HTTP API (0.0.0.0:9603)
  version Print version

`)
}

func runInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	configPath := fs.String("config", setup.DefaultConfigPath, "path to write config.yaml")
	force := fs.Bool("force", false, "overwrite existing config")
	skipInstall := fs.Bool("skip-install", false, "do not apt/dnf install packages (still configures them)")
	_ = fs.Parse(args)

	result, err := setup.Run(setup.Options{
		ConfigPath:  *configPath,
		Force:       *force,
		SkipInstall: *skipInstall,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "goincus init failed: %v\n", err)
		return 1
	}

	fmt.Println()
	fmt.Println("goincus init complete")
	fmt.Println("---------------------")
	fmt.Printf("Config:        %s\n", result.ConfigPath)
	fmt.Printf("Database port: 127.0.0.1:9601\n")
	redisNote := ""
	if result.RedisReused {
		redisNote = " (existing, reused)"
	}
	fmt.Printf("Redis:         %s:%d%s\n", result.RedisHost, result.RedisPort, redisNote)
	fmt.Printf("API listen:    0.0.0.0:9603\n")
	fmt.Println()
	fmt.Println("Generated secrets (store securely — shown once):")
	fmt.Printf("  database.password = %s\n", result.DBPassword)
	if result.RedisReused {
		if result.RedisPass == "" {
			fmt.Printf("  redis.password    = (none — existing Redis has no requirepass)\n")
		} else {
			fmt.Printf("  redis.password    = %s (from existing Redis)\n", result.RedisPass)
		}
	} else {
		fmt.Printf("  redis.password    = %s\n", result.RedisPass)
	}
	for i, key := range result.APIKeys {
		fmt.Printf("  auth.api_keys[%d]  = %s\n", i, key)
	}
	fmt.Println()
	fmt.Println("Start the API:")
	fmt.Println("  systemctl enable --now goincus")
	fmt.Println("  # or: goincus serve")
	return 0
}

func runServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", setup.DefaultConfigPath, "path to goincus config file")
	migrationsDir := fs.String("migrations", setup.MigrationsDir, "path to SQL migrations")
	_ = fs.Parse(args)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("load config", "path", *configPath, "err", err)
		return 1
	}
	if err := config.EnsureFilePermissions(*configPath); err != nil {
		logger.Error("insecure config permissions", "path", *configPath, "err", err)
		return 1
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	store, err := db.Connect(ctx, cfg.Database)
	if err != nil {
		logger.Error("database connect", "addr", fmt.Sprintf("%s:%d", cfg.Database.Host, cfg.Database.Port), "err", err)
		return 1
	}
	defer store.Close()

	migDir := *migrationsDir
	if abs, err := filepath.Abs(migDir); err == nil {
		migDir = abs
	}
	if err := store.Migrate(ctx, migDir); err != nil {
		logger.Error("migrate database", "dir", migDir, "err", err)
		return 1
	}

	rdb, err := redisstore.Connect(ctx, cfg.Redis)
	if err != nil {
		logger.Error("redis connect", "addr", cfg.Redis.Addr(), "err", err)
		return 1
	}
	defer func() { _ = rdb.Close() }()

	incusCli, err := incusclient.Connect(cfg.Incus)
	if err != nil {
		logger.Error("incus connect", "err", err)
		return 1
	}
	if pool := incusCli.ActiveStoragePool(); pool != "" && pool != cfg.Incus.StoragePool {
		cfg.Incus.StoragePool = pool
		if err := config.Save(*configPath, cfg); err != nil {
			logger.Warn("persist storage_pool", "pool", pool, "err", err)
		} else {
			logger.Info("updated config storage_pool", "pool", pool, "driver", incusCli.StoragePoolDriver())
		}
	}

	allocator := ports.NewAllocator(cfg.Ports, store, rdb)
	if err := allocator.Sync(ctx); err != nil {
		logger.Error("sync port allocator", "err", err)
		return 1
	}

	svc := service.New(cfg, store, rdb, incusCli, allocator, logger)
	srv := api.New(cfg, svc)

	httpServer := &http.Server{
		Addr:         cfg.Server.Addr(),
		Handler:      srv.Router(),
		ReadTimeout:  cfg.Server.ReadTimeout.Duration(),
		WriteTimeout: cfg.Server.WriteTimeout.Duration(),
		IdleTimeout:  cfg.Server.IdleTimeout.Duration(),
	}

	go func() {
		logger.Info("goincus listening",
			"addr", cfg.Server.Addr(),
			"database", fmt.Sprintf("%s:%d", cfg.Database.Host, cfg.Database.Port),
			"redis", cfg.Redis.Addr(),
			"storage_pool", incusCli.ActiveStoragePool(),
			"storage_driver", incusCli.StoragePoolDriver(),
		)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server", "err", err)
			cancel()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown", "err", err)
		return 1
	}
	return 0
}
