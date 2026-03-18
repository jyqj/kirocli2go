package tokenutil

import (
	"strings"
	"sync"

	"github.com/tiktoken-go/tokenizer"
)

var (
	encoderOnce sync.Once
	encoder     tokenizer.Codec
)

func CountText(text string) int {
	if text == "" {
		return 0
	}

	normalized := strings.ReplaceAll(text, "<thinking>", "")
	normalized = strings.ReplaceAll(normalized, "</thinking>", "")

	enc := getEncoder()
	if enc == nil {
		return fallbackCount(normalized)
	}

	tokens, _, err := enc.Encode(normalized)
	if err != nil {
		return fallbackCount(normalized)
	}
	return len(tokens)
}

func getEncoder() tokenizer.Codec {
	encoderOnce.Do(func() {
		enc, err := tokenizer.Get(tokenizer.Cl100kBase)
		if err == nil {
			encoder = enc
		}
	})
	return encoder
}

func fallbackCount(text string) int {
	runes := []rune(text)
	tokens := len(runes) / 4
	if tokens <= 0 {
		return 1
	}
	return tokens
}
