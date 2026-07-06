// Copyright 2026 Mustafa Al-Aqrawi (Smile Spoon). All rights reserved.
// Use of this source code is governed by the MIT License
// that can be found in the LICENSE file.

// NexusGuard AI - Ultra-Fast Local Reverse Proxy Server for AI Providers
// Author: Mustafa Al-Aqrawi (Smile Spoon)
//
// An intelligent, zero-config reverse proxy that sits between your code
// and any AI provider. Features semantic caching, PII masking, budget
// controls, and smart auto-fallback — all with a stunning TUI dashboard.

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/smilespoon/nexusguard-ai/pkg/config"
	"github.com/smilespoon/nexusguard-ai/pkg/proxy"
	"github.com/smilespoon/nexusguard-ai/pkg/tui"
	"github.com/smilespoon/nexusguard-ai/pkg/cache"
	"github.com/smilespoon/nexusguard-ai/pkg/budget"
	"github.com/smilespoon/nexusguard-ai/pkg/mask"
	"go.uber.org/zap"
)

var (
	version   = "dev"
	buildTime = "unknown"
	gitCommit = "unknown"
)

func main() {
	var (
		configPath = flag.String("config", "", "Path to configuration file")
		showTUI    = flag.Bool("tui", true, "Launch interactive TUI dashboard")
		port       = flag.String("port", "8080", "Proxy server port")
		daemonMode = flag.Bool("daemon", false, "Run in daemon mode (no TUI)")
		showVer    = flag.Bool("version", false, "Show version information")
	)
	flag.Parse()

	if *showVer {
		fmt.Printf("NexusGuard AI v%s (built: %s, commit: %s)\n", version, buildTime, gitCommit)
		fmt.Println("Author: Mustafa Al-Aqrawi (Smile Spoon)")
		os.Exit(0)
	}

	// Initialize structured logging
	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer logger.Sync()

	logger.Info("NexusGuard AI starting up",
		zap.String("version", version),
		zap.String("author", "Mustafa Al-Aqrawi (Smile Spoon)"),
	)

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Warn("Using default configuration", zap.Error(err))
		cfg = config.Default()
	}

	if *port != "8080" {
		cfg.Server.Port = *port
	}

	// Initialize subsystems
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize cache
	cacheManager, err := cache.New(cfg.Cache)
	if err != nil {
		logger.Error("Failed to initialize cache", zap.Error(err))
		os.Exit(1)
	}
	defer cacheManager.Close()

	// Initialize budget tracker
	budgetTracker := budget.New(cfg.Budget)

	// Initialize PII masker
	piiMasker := mask.New(cfg.Mask)

	// Create proxy server
	proxyServer, err := proxy.New(cfg, cacheManager, budgetTracker, piiMasker, logger)
	if err != nil {
		logger.Error("Failed to create proxy server", zap.Error(err))
		os.Exit(1)
	}

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		logger.Info("Shutdown signal received, gracefully stopping...")
		cancel()
	}()

	// Start proxy server in background
	go func() {
		logger.Info("Starting proxy server", zap.String("port", cfg.Server.Port))
		if err := proxyServer.Start(ctx); err != nil {
			logger.Error("Proxy server error", zap.Error(err))
		}
	}()

	// Launch TUI or run in daemon mode
	if *daemonMode || !*showTUI {
		logger.Info("Running in daemon mode")
		<-ctx.Done()
	} else {
		// Launch the breathtaking TUI dashboard
		tuiModel := tui.New(proxyServer, cacheManager, budgetTracker, piiMasker, version)
		if err := tui.Run(ctx, tuiModel); err != nil {
			logger.Error("TUI error", zap.Error(err))
		}
	}

	logger.Info("NexusGuard AI shutdown complete. See you soon!")
}
