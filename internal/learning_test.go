package internal_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/fmndantas/payments/internal/usecases/outbox"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestNilUuid(t *testing.T) {
	if foo := uuid.Nil; foo != (uuid.UUID{}) {
		t.Errorf("uuid.UUID{} != uuid.Nil. foo = %v", foo)
	}
}

func TestUnmarshallingOk(t *testing.T) {
	var payload outbox.PspSuccessPayload
	payloadString := fmt.Sprintf("{ \"id_psp_payment\": \"%s\" }", uuid.New().String())
	err := json.Unmarshal([]byte(payloadString), &payload)
	require.NoError(t, err)
}

func TestUnmarshallingError(t *testing.T) {
	var payload outbox.PspSuccessPayload
	payloadString := "foo"
	err := json.Unmarshal([]byte(payloadString), &payload)
	require.Error(t, err)
}

func TestFetchForecast_TimesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()

		if _, err := FetchForecast(ctx); err == nil {
			t.Fatal("expected a timeout error, got nil")
		}
	})
}

func FetchForecast(ctx context.Context) (string, error) {
	select {
	case <-time.After(time.Second):
		return "sunny", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
