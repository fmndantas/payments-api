package controller_test

import (
	"testing"

	sut "github.com/fmndantas/payments/internal/controller"
)

func TestGetIdRequestForPersistence(t *testing.T) {
	cases := []struct {
		kase              string
		originalIdRequest string
		expectedResult    string
		isOk              bool
	}{
		{"1", "request:checkout:something", "something", true},
		{"2", "something", "", false},
	}
	for _, tt := range cases {
		t.Run(tt.kase, func(t *testing.T) {
			result, err := sut.GetIdRequestForPersistence(tt.originalIdRequest)
			if tt.isOk {
				if err != nil {
					t.Fatalf("case %s: expected to be ok", tt.kase)
				} else if result != tt.expectedResult {
					t.Fatalf("case %s: expected %s, got %s", tt.kase, tt.expectedResult, result)
				}
			} else {
				if err == nil {
					t.Fatalf("case %s: expected to be error", tt.kase)
				}
			}
		})
	}
}
