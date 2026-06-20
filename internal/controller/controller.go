package controller

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/fmndantas/payments/internal/dependencies"
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

func CheckHealth(tree *dependencies.Tree, c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"version": "development"})
}

func Checkout(t *dependencies.Tree, context *gin.Context) {
	now := time.Now()

	var checkoutRequest *CheckoutRequest
	if err := context.ShouldBindJSON(&checkoutRequest); err != nil {
		context.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	idRequestDb, err := GetIdRequestForPersistence(checkoutRequest.IdRequest)

	if err != nil {
		context.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var foo string
	err = t.DbPool.QueryRow(context, "select id_request from payment where id_request = $1", idRequestDb).Scan(&foo)

	if err == nil {
		context.JSON(http.StatusConflict, gin.H{"error": "this request was already received"})
		return
	}

	accountsRows, err := t.DbPool.Query(
		context,
		"select id_internal, id_external from account where id_external = $1 or id_external = $2",
		checkoutRequest.IdSourceAccount,
		checkoutRequest.IdDestinyAccount,
	)

	if err != nil {
		context.JSON(http.StatusInternalServerError, gin.H{"error": "error when fetching accounts"})
		return
	}

	defer accountsRows.Close()

	var idInternalSourceAccount, idInternalDestinyAccount int
	for accountsRows.Next() {
		var idInternal int
		var idExternal string
		accountsRows.Scan(&idInternal, &idExternal)
		if idExternal == checkoutRequest.IdSourceAccount {
			idInternalSourceAccount = idInternal
		}
		if idExternal == checkoutRequest.IdDestinyAccount {
			idInternalDestinyAccount = idInternal
		}
	}

	if idInternalSourceAccount == 0 || idInternalDestinyAccount == 0 {
		context.JSON(http.StatusBadRequest, gin.H{"error": "at least one account is missing"})
		return
	}

	idExternalPayment, idInternalPayment := uuid.New(), 0

	tx, err := t.DbPool.Begin(context)

	if err != nil {
		context.JSON(http.StatusInternalServerError, gin.H{"error": "error when initializing the transaction"})
		return
	}

	_ = tx.QueryRow(
		context,
		`insert into payment(id_external, id_request, id_source_account, id_destiny_account, is_pending, created_at)
		values ($1, $2, $3, $4, $5, $6)
		returning id_internal`,
		idExternalPayment, idRequestDb, idInternalSourceAccount, idInternalDestinyAccount, true, now,
	).Scan(&idInternalPayment)

	log.Printf("idInternalPayment: %d", idInternalPayment)

	_, err = tx.Exec(
		context,
		`insert into outbox (id_payment, is_pending, next_try_at, created_at)
		values ($1, $2, $3, $4)`,
		idInternalPayment, true, now, now,
	)

	defer tx.Rollback(context)

	if err != nil {
		log.Panicf("%s", err)
		return
	}

	if err := tx.Commit(context); err != nil {
		log.Panicf("%s", err)
		return
	}

	context.JSON(http.StatusOK, gin.H{"id_payment": idExternalPayment})
}
