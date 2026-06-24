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

	"github.com/fmndantas/payments/testingutils"

	"github.com/fmndantas/payments"
	"github.com/fmndantas/payments/internal/controller"
)

type CheckoutResponse struct {
	IdPayment string `json:"id_payment"`
}

// TODO: better to seed data and get ids?
const (
	idSourceAccount  string = "e4215def-6f52-4f3a-8cd7-23e261bad9e7"
	IdDestinyAccount string = "597cb0af-0562-496b-9802-94dc5b0f082d"
)

// TODO: having this global variable is a anti-pattern?
var (
	router *gin.Engine
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	postgres, err, stopPostgres := containers.InitializePostgresTestcontainer(ctx)
	if err != nil {
		log.Panicf("failed to start PostgreSQL container: %s", err)
	}
	connectionString, err := postgres.ConnectionString(ctx)
	router = main.Initialize(connectionString, true)
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
	if w.Code != expectedStatusCode {
		t.Errorf("expected http response to be %d, but was %d", expectedStatusCode, w.Code)
		t.Errorf("%v", w.Body)
	}
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
		IdSourceAccount:  idSourceAccount,
		IdDestinyAccount: IdDestinyAccount,
		IdRequest:        fmt.Sprintf("request:checkout:%s", uuid.NewString()),
	}
	payloadJson, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", "/checkout", bytes.NewBuffer(payloadJson))
	router.ServeHTTP(w, req)
	expectStatusCode(t, w, 200)
	var checkoutResponse CheckoutResponse
	err := json.Unmarshal(w.Body.Bytes(), &checkoutResponse)
	if err != nil {
		t.Fatalf("response unmarshal resulted in an error")
	}
	if len(checkoutResponse.IdPayment) == 0 {
		t.Errorf("id_payment is empty")
	}
}
