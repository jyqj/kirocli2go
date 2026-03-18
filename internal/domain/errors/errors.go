package domainerrors

import (
	"fmt"
	"strings"
)

type Category string

const (
	CategoryAuth           Category = "auth_error"
	CategoryQuota          Category = "quota_error"
	CategoryBan            Category = "ban_error"
	CategoryNetwork        Category = "network_error"
	CategoryValidation     Category = "validation_error"
	CategoryNotImplemented Category = "not_implemented"
	CategoryUnknown        Category = "unknown_error"
)

type UpstreamError struct {
	Category   Category
	Signal     string
	Message    string
	StatusCode int
	Retryable  bool
	Cause      error
}

func (e *UpstreamError) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause == nil {
		return fmt.Sprintf("%s: %s", e.Category, e.Message)
	}
	return fmt.Sprintf("%s: %s: %v", e.Category, e.Message, e.Cause)
}

func New(category Category, message string) *UpstreamError {
	return &UpstreamError{
		Category: category,
		Message:  message,
	}
}

func DetectSignal(text string) (Category, string, bool) {
	normalized := strings.ToUpper(strings.TrimSpace(text))
	if normalized == "" {
		return "", "", false
	}

	switch {
	case strings.Contains(normalized, "TEMPORARILY_SUSPENDED"):
		return CategoryBan, "TEMPORARILY_SUSPENDED", true
	case strings.Contains(normalized, "MONTHLY_REQUEST_COUNT"),
		strings.Contains(normalized, "QUOTA"),
		strings.Contains(normalized, "QUOTA_EXCEEDED"):
		if strings.Contains(normalized, "MONTHLY_REQUEST_COUNT") {
			return CategoryQuota, "MONTHLY_REQUEST_COUNT", true
		}
		return CategoryQuota, "QUOTA", true
	case strings.Contains(normalized, "UNAUTHORIZED"),
		strings.Contains(normalized, "INVALID_TOKEN"),
		strings.Contains(normalized, "TOKEN_EXPIRED"),
		strings.Contains(normalized, "ACCESS DENIED"),
		strings.Contains(normalized, "ACCESS_DENIED"):
		if strings.Contains(normalized, "TOKEN_EXPIRED") {
			return CategoryAuth, "TOKEN_EXPIRED", true
		}
		if strings.Contains(normalized, "INVALID_TOKEN") {
			return CategoryAuth, "INVALID_TOKEN", true
		}
		return CategoryAuth, "ACCESS_DENIED", true
	default:
		return "", "", false
	}
}
