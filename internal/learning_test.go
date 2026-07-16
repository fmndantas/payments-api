package internal_test

import (
	"testing"

	"github.com/google/uuid"
)

func TestNilUuid(t *testing.T) {
	if foo := uuid.Nil; foo != (uuid.UUID{}) {
		t.Errorf("uuid.UUID{} != uuid.Nil. foo = %v", foo)
	}
}
