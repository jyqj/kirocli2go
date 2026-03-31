package shared

import (
	"net/http"
	"strings"
)

func SessionKeyFrom(r *http.Request) string {
	if r == nil {
		return ""
	}
	for _, key := range []string{
		"X-Kiro-Session-Id",
		"X-Session-Id",
		"X-Claude-Code-Session-Id",
	} {
		if value := strings.TrimSpace(r.Header.Get(key)); value != "" {
			return value
		}
	}
	return ""
}

func CompactRequestedFrom(r *http.Request) bool {
	if r == nil {
		return false
	}
	for _, key := range []string{
		"X-Kiro-Compact",
		"X-Compact",
	} {
		switch strings.ToLower(strings.TrimSpace(r.Header.Get(key))) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

func WorkingDirectoryFrom(r *http.Request) string {
	if r == nil {
		return ""
	}
	for _, key := range []string{
		"X-Kiro-Workdir",
		"X-Workspace-Path",
		"X-Working-Directory",
	} {
		if value := strings.TrimSpace(r.Header.Get(key)); value != "" {
			return value
		}
	}
	return ""
}
