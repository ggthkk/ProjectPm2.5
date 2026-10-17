package main

import (
	"context"
	"flag"
	"github.com/joho/godotenv"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	_ "time/tzdata"
)

func main() {
	mode := flag.String("mode", "daemon", "daemon, once, check, or health")
	dry := flag.Bool("dry-run", false, "preview without sending or changing state")
	previewDir := flag.String("preview-dir", "", "save PNG cards during dry-run")
	flag.Parse()
	if *previewDir != "" && !*dry {
		log.Fatal("preview-dir requires dry-run")
	}
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Fatal("cannot load .env")
	}
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	if *mode == "health" {
		info, err := os.Stat(filepath.Join(cfg.DataDir, "heartbeat"))
		if err != nil || time.Since(info.ModTime()) > 10*time.Minute {
			os.Exit(1)
		}
		return
	}
	if *mode != "daemon" && *mode != "once" && *mode != "check" {
		log.Fatal("invalid mode")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	app, err := newApp(cfg, *dry || *mode == "check")
	if err != nil {
		log.Fatal(err)
	}
	app.previewDir = *previewDir
	if *mode == "check" {
		if err := app.check(ctx); err != nil {
			log.Print(err)
			os.Exit(1)
		}
		return
	}
	if *mode == "once" {
		if err := app.tick(ctx, time.Now(), true); err != nil {
			log.Print(err)
			os.Exit(1)
		}
		return
	}
	log.Printf("bot started poll=%s digest=%s timezone=%s dry_run=%t", cfg.Poll, cfg.DigestTime, cfg.Zone, app.dry)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := app.tick(ctx, time.Now(), false); err != nil {
			log.Print(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
