package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fmndantas/payments/cmd/api"
	"github.com/fmndantas/payments/test"

	"github.com/fmndantas/payments/internal/controller"
	"github.com/fmndantas/payments/internal/db"
)

type CheckoutResponse struct {
	IdPayment string `json:"id_payment"`
}

var (
	router *gin.Engine
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	dbLocalConfiguration := db.CreateLocalConfiguration()
	container, err, stopPostgres := test.InitializePostgresTestcontainer(dbLocalConfiguration, ctx)
	if err != nil {
		log.Fatalf("failed to start PostgreSQL container: %s", err)
	}
	host, err := container.Host(ctx)
	if err != nil {
		log.Fatalf("failed to get testcontainer host: %s", err)
	}
	port, err := container.MappedPort(ctx, fmt.Sprintf("%d", dbLocalConfiguration.Port))
	if err != nil {
		log.Fatalf("failed to get testcontainer mapped port: %s", err)
	}
	dbTestConfiguration := db.DbConfiguration{
		Host:     host,
		Port:     int(port.Num()),
		Database: dbLocalConfiguration.Database,
		Username: dbLocalConfiguration.Username,
		Password: dbLocalConfiguration.Password,
	}
	router, err = main.Initialize(dbTestConfiguration, true)
	if err != nil {
		log.Fatal(err)
	}
	code := m.Run()
	router = nil
	stopPostgres()
	os.Exit(code)
}

func expectStatusCode(
	t *testing.T,
	w *httptest.ResponseRecorder,
	expectedStatusCode int,
) {
	assert.Equal(t, expectedStatusCode, w.Code, "wrong HTTP response status code")
}

func TestHealthEndpoint(t *testing.T) {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/health", nil)
	router.ServeHTTP(w, req)
	expectStatusCode(t, w, 200)
}

func TestCheckoutWithoutBody(t *testing.T) {
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/checkout", nil)
	router.ServeHTTP(w, req)
	expectStatusCode(t, w, 400)
}

func TestCheckoutHappyPath(t *testing.T) {
	w := httptest.NewRecorder()
	payload := controller.CheckoutRequest{
		IdSourceAccount:  test.IdSourceAccountAsString,
		IdDestinyAccount: test.IdDestinyAccountAsString,
		IdRequest:        fmt.Sprintf("request:checkout:%s", uuid.NewString()),
	}
	payloadJson, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", "/checkout", bytes.NewBuffer(payloadJson))
	router.ServeHTTP(w, req)
	expectStatusCode(t, w, 200)
	var checkoutResponse CheckoutResponse
	err := json.Unmarshal(w.Body.Bytes(), &checkoutResponse)
	require.NoError(t, err, "response unmarshal resulted in an error")
	assert.Positive(t, checkoutResponse.IdPayment, "id_payment is not positive")
}

func TestCheckoutIdempotency(t *testing.T) {
	payload := controller.CheckoutRequest{
		IdSourceAccount:  test.IdSourceAccountAsString,
		IdDestinyAccount: test.IdDestinyAccountAsString,
		IdRequest:        fmt.Sprintf("request:checkout:%s", uuid.NewString()),
	}
	payloadJson, _ := json.Marshal(payload)
	for i := range 5 {
		req, _ := http.NewRequest("POST", "/checkout", bytes.NewBuffer(payloadJson))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if i == 0 {
			// first time, gets 200
			expectStatusCode(t, w, 200)
		} else {
			// repetead id_request avoids reprocessings
			expectStatusCode(t, w, 409)
		}
	}
}
