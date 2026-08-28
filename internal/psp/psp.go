package psp

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"math/rand/v2"

	"github.com/fmndantas/payments/internal/db"
	"github.com/fmndantas/payments/internal/resilience"
)

type PspInput struct {
	Context context.Context
	Outbox  db.Outbox
}

type PspHttpResponse struct {
	StatusCode int
	JsonBody   string
}

type PspOutput struct {
	HttpResponse  PspHttpResponse
	Error error
}

type SendEventToPspFn = resilience.DoRequest[PspInput, PspOutput]

// This function simulates the PSP response
func SendOutboxEventToPspFake(pspInput PspInput) PspOutput {
	if pspInput.Context.Err() != nil {
		return PspOutput{Error: pspInput.Context.Err()}
	}

	randomValue := rand.IntN(100)

	if randomValue > 75 {
		return PspOutput{
			HttpResponse: PspHttpResponse{
				StatusCode: 500,
				JsonBody:   "{ \"error\": \"the server couldn't process the request\" }",
			},
		}
	} else if randomValue > 50 {
		return PspOutput{
			HttpResponse: PspHttpResponse{
				StatusCode: 429,
				JsonBody:   "{ \"error\": \"the server is busy\" }",
			},
		}
	} else if randomValue > 25 {
		return PspOutput{
			Error: errors.New("this is an unexpected error"),
		}
	} else {
		return PspOutput{
			HttpResponse: PspHttpResponse{
				StatusCode: 202,
				JsonBody:   fmt.Sprintf("{ \"id_psp_payment\": \"%s\" }", uuid.New().String()),
			},
		}
	}
}
