package usecases

import (
	"github.com/fmndantas/payments/internal/dependencies"
	"github.com/google/uuid"
)

func HandleCheckout(
	tree *dependencies.Tree, idRequest, idSourceAccount, idDestinyAccount uuid.UUID,
) (uuid.UUID, error) {
	panic("TODO")
}
