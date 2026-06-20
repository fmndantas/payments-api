package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/fmndantas/payments/internal/dependencies"
)

type CheckoutRequest struct {
	IdSourceAccount  string
	IdDestinyAccount string
	IdRequest        string
}

func CheckHealth(t *dependencies.Tree, c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"version": "development"})
}

func Checkout(t *dependencies.Tree, c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"id_payment": "TODO"})
}


