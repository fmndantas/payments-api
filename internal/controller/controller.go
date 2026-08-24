package controller

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/fmndantas/payments/internal/dependencies"
	"github.com/fmndantas/payments/internal/usecases"
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

func Health(tree *dependencies.Tree, c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"version": "development"})
}

func Checkout(t *dependencies.Tree, context *gin.Context) {
	var request *CheckoutRequest
	if err := context.ShouldBindJSON(&request); err != nil {
		context.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	idRequestWithoutPrefix, err := GetIdRequestForPersistence(request.IdRequest)
	if err != nil {
		context.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
		context.JSON(http.StatusBadRequest, gin.H{"error": validation.Error()})
		return
	}

	idExternalPayment, err := usecases.HandleCheckout(
		t, context, idRequest, idSourceAccount, idDestinyAccount, time.Now(),
	)

	if err != nil {
		var statusCode int
		switch {
		case errors.Is(err, usecases.ErrorCheckoutAtLeastOneAccountIsMissing):
			statusCode = http.StatusBadRequest
		case errors.Is(err, usecases.ErrorCheckoutIdRequestAlreadyWasProcessed):
			statusCode = http.StatusConflict
		default:
			statusCode = http.StatusInternalServerError
		}
		context.JSON(statusCode, gin.H{"error": err.Error()})
		return
	}

	context.JSON(http.StatusOK, gin.H{"id_payment": idExternalPayment})
}
