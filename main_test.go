package main_test

import (
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/fmndantas/payments"
)

// TODO: having this global variable is a anti-pattern?
var router *gin.Engine

func TestMain(m *testing.M) {
	dbName, dbUser, dbPassword := "payments", "postgres", "postgres"
	ctx := context.Background()
	container, err := postgres.Run(
		ctx,
		"postgres",
		postgres.WithOrderedInitScripts(
			filepath.Join(filepath.Join("migrations", "001_initial_migration.sql")),
			filepath.Join(filepath.Join("migrations", "002_create_accounts.sql")),
		),
		postgres.WithDatabase(dbName),
		postgres.WithUsername(dbUser),
		postgres.WithPassword(dbPassword),
		postgres.BasicWaitStrategies(),
	)
	defer func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			log.Printf("failed to terminate container: %s", err)
		}
	}()
	if err != nil {
		log.Panicf("failed to start PostgreSQL container: %s", err)
	}
	connectionString, err := container.ConnectionString(ctx)
	router = main.Initialize(connectionString, true)
	code := m.Run()
	router = nil
	os.Exit(code)
}

func TestHealthEndpoint(t *testing.T) {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/health", nil)
	router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Error("returned response was not ok")
	}
}
