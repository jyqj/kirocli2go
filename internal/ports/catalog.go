package ports

import (
	"context"

	"kirocli-go/internal/domain/model"
)

type ModelCatalog interface {
	Resolve(ctx context.Context, externalModel string) (model.ResolvedModel, error)
	List(ctx context.Context) ([]model.ResolvedModel, error)
}
