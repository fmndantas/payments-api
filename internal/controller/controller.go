package controller

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/fmndantas/payments/internal/dependencies"
	"github.com/fmndantas/payments/internal/usecases/checkout"
)

type CheckoutRequest struct {
	IdSourceAccount  string `json:"id_source_account" binding:"required"`
	IdDestinyAccount string `json:"id_destiny_account" binding:"required"`
	IdRequest        string `json:"id_request" binding:"required"`
}

func GetIdRequestForPersistence(originalIdRequest string) (string, error) {
	if !strings.HasPrefix(originalIdRequest, "request:checkout:") {
		return "", errors.New("the prefix \"request:checkout:\" is missing")
	}
	return strings.Replace(originalIdRequest, "request:checkout:", "", 1), nil
}

func Health(tree *dependencies.Tree, ctx *gin.Context) {
	ctx.JSON(http.StatusOK, gin.H{"version": "development"})
}

func Checkout(t *dependencies.Tree, ctx *gin.Context) {
	var request *CheckoutRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	idRequestWithoutPrefix, err := GetIdRequestForPersistence(request.IdRequest)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var validation error
	idRequest, err := uuid.Parse(idRequestWithoutPrefix)
	validation = errors.Join(validation, err)

	idSourceAccount, err := uuid.Parse(request.IdSourceAccount)
	validation = errors.Join(validation, err)

	idDestinyAccount, err := uuid.Parse(request.IdDestinyAccount)
	validation = errors.Join(validation, err)

	if validation != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": validation.Error()})
		return
	}

	idExternalPayment, err := checkout.HandleCheckout(
		t, ctx, idRequest, idSourceAccount, idDestinyAccount, time.Now(),
	)

	if err != nil {
		var statusCode int
		switch {
		case errors.Is(err, checkout.ErrorCheckoutAtLeastOneAccountIsMissing):
			statusCode = http.StatusBadRequest
		case errors.Is(err, checkout.ErrorCheckoutIdRequestAlreadyWasProcessed):
			statusCode = http.StatusConflict
		default:
			statusCode = http.StatusInternalServerError
		}
		ctx.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"id_payment": idExternalPayment})
}
