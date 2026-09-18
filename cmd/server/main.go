package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"hrreport/internal/app"
	"hrreport/migrations"
	rediscache "hrreport/pkg/cache/redis"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Print("no .env file found, using system env or defaults")
	}

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
			departments, vacancies, err := app.CheckDebtsterAPI(ctx, debtsterURL(mock), debtsterKey(mock), mock)
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
		DebtsterKey:  debtsterKey(mock),
		DebtsterMock: mock,
		HTTPAddr:     require("HTTP_ADDR"),
		StaticDir:    require("STATIC_DIR"),
		AppSecret:    require("APP_SECRET"),
		SuperLogin:   require("SUPERADMIN_LOGIN"),
		SuperPass:    require("SUPERADMIN_PASSWORD"),
		RedisPrefix:  getenv("REDIS_PREFIX", "debtster_database"),
		Redis: rediscache.ConnectionInfo{
			Addr:        getenv("REDIS_ADDR", "127.0.0.1:6379"),
			Password:    getenv("REDIS_PASSWORD", "hello-world"),
			DB:          requireInt("REDIS_DB", 0),
			MaxRetries:  requireInt("REDIS_MAX_RETRIES", 5),
			DialTimeout: time.Duration(requireInt("REDIS_DIAL_TIMEOUT", 10)) * time.Second,
			Timeout:     time.Duration(requireInt("REDIS_TIMEOUT", 5)) * time.Second,
		},
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

func debtsterKey(mock bool) string {
	if mock {
		return ""
	}
	return require("DEBSTER_KEY")
}

func require(name string) string {
	v := os.Getenv(name)
	if v == "" {
		log.Fatalf("required environment variable %s is not set", name)
	}
	return v
}

func getenv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func requireInt(name string, fallback int) int {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		log.Fatalf("invalid int value %q for %s: %v", value, name, err)
	}
	return parsed
}
