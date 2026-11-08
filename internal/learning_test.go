package internal_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/fmndantas/payments/internal/usecases/outbox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
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

func TestNilErrorIsNotASentinel(t *testing.T) {
	assert.False(t, errors.Is(nil, pgx.ErrNoRows))
}
