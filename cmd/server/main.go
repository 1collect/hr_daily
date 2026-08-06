package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"hrreport/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := app.Config{
		DatabaseURL: require("DATABASE_URL"),
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
