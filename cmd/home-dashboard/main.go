package main

import (
	"context"
	"flag"
	"fmt"
	"home-dashboard/internal/dashboard"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	_ "time/tzdata"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func main() {
	addr := flag.String("addr", env("DASHBOARD_ADDR", "127.0.0.1:4173"), "HTTP listen address")
	dbpath := flag.String("db", env("DASHBOARD_DB", "data/dashboard.sqlite"), "SQLite file")
	tz := flag.String("timezone", env("DASHBOARD_TIMEZONE", "America/New_York"), "Household time zone")
	demo := flag.Bool("demo", false, "Seed example data in a new database")
	health := flag.Bool("healthcheck", false, "Check the running local server and exit without opening SQLite")
	backup := flag.String("backup", "", "Write a consistent SQLite backup and exit")
	flag.Parse()
	if *health {
		if err := checkHealth(*addr); err != nil {
			log.Fatal(err)
		}
		return
	}
	loc, err := time.LoadLocation(*tz)
	if err != nil {
		log.Fatal(err)
	}
	store, err := dashboard.Open(*dbpath, *demo, time.Now().In(loc).Format("2006-01-02"))
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	if *backup != "" {
		if err := store.Backup(*backup); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Backup:", *backup)
		return
	}
	app, err := dashboard.New(store, loc)
	if err != nil {
		log.Fatal(err)
	}
	weatherCtx, cancelWeather := context.WithCancel(context.Background())
	weatherDone := make(chan struct{})
	go func() { defer close(weatherDone); app.RunWeather(weatherCtx) }()
	sportsDone := make(chan struct{})
	go func() { defer close(sportsDone); app.RunSports(weatherCtx) }()
	mediaDone := make(chan struct{})
	go func() { defer close(mediaDone); app.RunMedia(weatherCtx) }()
	spotifyDone := make(chan struct{})
	go func() { defer close(spotifyDone); app.RunSpotify(weatherCtx) }()
	defer func() { cancelWeather(); <-weatherDone; <-sportsDone; <-mediaDone; <-spotifyDone }()
	server := &http.Server{Addr: *addr, Handler: app, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 16}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Print(err)
		}
	}()
	absolute, _ := filepath.Abs(*dbpath)
	log.Printf("Home HQ listening at http://%s · database %s · zone %s", *addr, absolute, loc)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
