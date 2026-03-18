package noop

import (
	"context"
	"io"

	domainerrors "kirocli-go/internal/domain/errors"
	"kirocli-go/internal/domain/message"
	"kirocli-go/internal/ports"
)

type Formatter struct {
	Name string
}

func New(name string) *Formatter {
	return &Formatter{Name: name}
}

func (f *Formatter) FormatStream(ctx context.Context, req message.UnifiedRequest, upstream ports.UpstreamStream, w io.Writer) error {
	_ = ctx
	_ = req
	_ = upstream
	_ = w
	return domainerrors.New(domainerrors.CategoryNotImplemented, "stream formatter "+f.Name+" is not implemented")
}

func (f *Formatter) FormatJSON(ctx context.Context, req message.UnifiedRequest, upstream ports.UpstreamStream, w io.Writer) error {
	_ = ctx
	_ = req
	_ = upstream
	_ = w
	return domainerrors.New(domainerrors.CategoryNotImplemented, "json formatter "+f.Name+" is not implemented")
}
