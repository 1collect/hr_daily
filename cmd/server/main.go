package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
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
			mock := debtsterMock()
			departments, vacancies, err := app.CheckDebtsterAPI(ctx, debtsterURL(mock), mock)
			if err != nil {
				log.Fatalf("Debtster API check failed: %v", err)
			}
			log.Printf("Debtster API check passed: %d departments and %d vacancy reports received", departments, vacancies)
			return
		}
	}
	if len(os.Args) != 1 {
		log.Fatalf("usage: %s [migrate|check-debtster]", os.Args[0])
	}

	mock := debtsterMock()
	cfg := app.Config{
		DatabaseURL:  require("DATABASE_URL"),
		DebtsterAPI:  debtsterURL(mock),
		DebtsterMock: mock,
		HTTPAddr:     require("HTTP_ADDR"),
		StaticDir:    require("STATIC_DIR"),
		AppSecret:    require("APP_SECRET"),
		SuperLogin:   require("SUPERADMIN_LOGIN"),
		SuperPass:    require("SUPERADMIN_PASSWORD"),
	}
	if err := app.Run(ctx, cfg); err != nil {
		log.Fatal(err)
	}
}

func debtsterMock() bool {
	value := os.Getenv("DEBTSTER_MOCK")
	if value == "" {
		return false
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		log.Fatal("DEBTSTER_MOCK must be true or false")
	}
	if enabled {
		log.Print("DEBTSTER_MOCK enabled: using demo data without requests to Debtster")
	}
	return enabled
}

func debtsterURL(mock bool) string {
	if mock {
		return "https://debtster.mock"
	}
	return require("DEBTSTER_API")
}

func require(name string) string {
	v := os.Getenv(name)
	if v == "" {
		log.Fatalf("required environment variable %s is not set", name)
	}
	return v
}
