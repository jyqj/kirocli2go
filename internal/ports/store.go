package ports

import (
	"context"

	"kirocli-go/internal/domain/account"
)

type AccountStore interface {
	ListAccounts(ctx context.Context) ([]account.Record, error)
	SaveAccount(ctx context.Context, record account.Record) error
	UpdateAccountStatus(ctx context.Context, id string, status account.Status) error
}
