package ports

import (
	"context"
	"io"

	"kirocli-go/internal/domain/message"
)

type ResponseFormat string

const (
	ResponseFormatOpenAI    ResponseFormat = "openai"
	ResponseFormatAnthropic ResponseFormat = "anthropic"
)

type Formatter interface {
	FormatStream(ctx context.Context, req message.UnifiedRequest, upstream UpstreamStream, w io.Writer) error
	FormatJSON(ctx context.Context, req message.UnifiedRequest, upstream UpstreamStream, w io.Writer) error
}
