package controller_test

import (
	"testing"

	sut "github.com/fmndantas/payments/internal/controller"
)

func TestGetIdRequestForPersistence(t *testing.T) {
	cases := []struct {
		idCase            string
		originalIdRequest string
		expectedResult    string
		isOk              bool
	}{
		{"1", "request:checkout:something", "something", true},
		{"2", "something", "", false},
		{"3", "request:something", "", false},
	}
	for _, tt := range cases {
		t.Run(tt.idCase, func(t *testing.T) {
			result, err := sut.GetIdRequestForPersistence(tt.originalIdRequest)
			if tt.isOk {
				if err != nil {
					t.Fatalf("case %s: expected to be ok", tt.idCase)
				} else if result != tt.expectedResult {
					t.Fatalf("case %s: expected %s, got %s", tt.idCase, tt.expectedResult, result)
				}
			} else {
				if err == nil {
					t.Fatalf("case %s: expected to be error", tt.idCase)
				}
			}
		})
	}
}
