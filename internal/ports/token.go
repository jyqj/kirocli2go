package ports

import (
	"context"

	"kirocli-go/internal/domain/account"
)

type TokenProvider interface {
	Acquire(ctx context.Context, hint account.AcquireHint) (account.Lease, error)
	ReportSuccess(ctx context.Context, lease account.Lease, meta account.SuccessMeta) error
	ReportFailure(ctx context.Context, lease account.Lease, meta account.FailureMeta) error
}
