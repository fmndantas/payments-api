package usecases

import (
	"github.com/google/uuid"

	"github.com/fmndantas/payments/internal/dependencies"
)

func HandleCheckout(
	tree *dependencies.Tree, idRequest, idSourceAccount, idDestinyAccount uuid.UUID,
) (uuid.UUID, error) {
	panic("TODO")
}
