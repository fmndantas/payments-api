package internal_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/fmndantas/payments/internal/usecases"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestNilUuid(t *testing.T) {
	if foo := uuid.Nil; foo != (uuid.UUID{}) {
		t.Errorf("uuid.UUID{} != uuid.Nil. foo = %v", foo)
	}
}

func TestUnmarshallingOk(t *testing.T) {
	var payload usecases.PspSuccessPayload
	payloadString := fmt.Sprintf("{ \"id_psp_payment\": \"%s\" }", uuid.New().String())
	err := json.Unmarshal([]byte(payloadString), &payload)
	require.NoError(t, err)
}

func TestUnmarshallingError(t *testing.T) {
	var payload usecases.PspSuccessPayload
	payloadString := "foo"
	err := json.Unmarshal([]byte(payloadString), &payload)
	require.Error(t, err)
}
