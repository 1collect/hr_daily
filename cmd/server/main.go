package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"hrreport/internal/app"
	"hrreport/migrations"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if len(os.Args) == 2 {
		switch os.Args[1] {
		case "migrate":
			db, err := pgxpool.New(ctx, require("DATABASE_URL"))
			if err != nil {
				log.Fatal(err)
			}
			defer db.Close()
			if err = migrations.Up(ctx, db); err != nil {
				log.Fatalf("migrations: %v", err)
			}
			log.Print("database migrations applied")
			return
		case "check-debtster":
			count, err := app.CheckDebtsterAPI(ctx, require("DEBTSTER_API"))
			if err != nil {
				log.Fatalf("Debtster API check failed: %v", err)
			}
			log.Printf("Debtster API check passed: %d departments received", count)
			return
		}
	}
	if len(os.Args) != 1 {
		log.Fatalf("usage: %s [migrate|check-debtster]", os.Args[0])
	}

	cfg := app.Config{
		DatabaseURL: require("DATABASE_URL"),
		DebtsterAPI: require("DEBTSTER_API"),
		HTTPAddr:    require("HTTP_ADDR"),
		StaticDir:   require("STATIC_DIR"),
		AppSecret:   require("APP_SECRET"),
		SuperLogin:  require("SUPERADMIN_LOGIN"),
		SuperPass:   require("SUPERADMIN_PASSWORD"),
	}
	if err := app.Run(ctx, cfg); err != nil {
		log.Fatal(err)
	}
}

func require(name string) string {
	v := os.Getenv(name)
	if v == "" {
		log.Fatalf("required environment variable %s is not set", name)
	}
	return v
}
