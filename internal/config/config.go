package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Server     ServerConfig
	Security   SecurityConfig
	Accounts   AccountsConfig
	Background BackgroundConfig
	State      StateConfig
	Models     ModelsConfig
	Upstream   UpstreamConfig
}

type ServerConfig struct {
	Address      string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
}

type SecurityConfig struct {
	APIToken string
}

type AccountsConfig struct {
	Source         string
	BearerToken    string
	CSVPath        string
	APIURL         string
	APIToken       string
	APICategoryID  int
	APIFetchCount  int
	ActivePoolSize int
	MaxRefreshTry  int
	OIDCURL        string
	RefreshTimeout time.Duration
	StatePath      string
}

type BackgroundConfig struct {
	ModelRefreshEnabled      bool
	ModelRefreshOnStart      bool
	ModelRefreshInterval     time.Duration
	ModelRefreshStartupDelay time.Duration
	TokenPoolEnabled         bool
	TokenPoolWarmOnStart     bool
	TokenPoolRefreshInterval time.Duration
	TokenPoolStartupDelay    time.Duration
}

type StateConfig struct {
	PersistEnabled  bool
	PersistInterval time.Duration
	StatsPath       string
	CatalogPath     string
}

type ModelsConfig struct {
	ThinkingSuffix string
}

type UpstreamConfig struct {
	CLIBaseURL      string
	CLIModelsURL    string
	CLIMCPURL       string
	CLIProxyURL     string
	CLIUserAgent    string
	CLIAmzUserAgent string
	CLIOrigin       string
	CLITarget       string
	CLIModelsTarget string
	CLITimeout      time.Duration
}

const Version = "0.1.0"

func FromEnv() Config {
	return Config{
		Server: ServerConfig{
			Address:      envOrDefault("KIROCLI_GO_ADDR", ":8089"),
			ReadTimeout:  durationEnv("KIROCLI_GO_READ_TIMEOUT_SEC", 15*time.Second),
			WriteTimeout: durationEnv("KIROCLI_GO_WRITE_TIMEOUT_SEC", 60*time.Second),
			IdleTimeout:  durationEnv("KIROCLI_GO_IDLE_TIMEOUT_SEC", 90*time.Second),
		},
		Security: SecurityConfig{
			APIToken: envOrDefault("KIROCLI_GO_API_TOKEN", ""),
		},
		Accounts: AccountsConfig{
			Source:         envOrDefault("KIROCLI_GO_ACCOUNT_SOURCE", "auto"),
			BearerToken:    envOrDefault("KIROCLI_GO_BEARER_TOKEN", ""),
			CSVPath:        envOrDefault("KIROCLI_GO_ACCOUNTS_CSV", ""),
			APIURL:         envOrDefault("KIROCLI_GO_ACCOUNT_API_URL", ""),
			APIToken:       envOrDefault("KIROCLI_GO_ACCOUNT_API_TOKEN", ""),
			APICategoryID:  intEnv("KIROCLI_GO_ACCOUNT_CATEGORY_ID", 3),
			APIFetchCount:  intEnv("KIROCLI_GO_ACCOUNT_FETCH_COUNT", 20),
			ActivePoolSize: intEnv("KIROCLI_GO_ACTIVE_POOL_SIZE", 10),
			MaxRefreshTry:  intEnv("KIROCLI_GO_MAX_REFRESH_ATTEMPTS", 3),
			OIDCURL:        envOrDefault("KIROCLI_GO_OIDC_URL", "https://oidc.us-east-1.amazonaws.com/token"),
			RefreshTimeout: durationEnv("KIROCLI_GO_REFRESH_TIMEOUT_SEC", 30*time.Second),
			StatePath:      envOrDefault("KIROCLI_GO_ACCOUNT_STATE_PATH", "data/accounts_state.json"),
		},
		Background: BackgroundConfig{
			ModelRefreshEnabled:      boolEnv("KIROCLI_GO_MODEL_REFRESH_ENABLED", true),
			ModelRefreshOnStart:      boolEnv("KIROCLI_GO_MODEL_REFRESH_ON_START", true),
			ModelRefreshInterval:     durationEnv("KIROCLI_GO_MODEL_REFRESH_INTERVAL_SEC", 30*time.Minute),
			ModelRefreshStartupDelay: durationEnv("KIROCLI_GO_MODEL_REFRESH_STARTUP_DELAY_SEC", 5*time.Second),
			TokenPoolEnabled:         boolEnv("KIROCLI_GO_TOKEN_POOL_ENABLED", true),
			TokenPoolWarmOnStart:     boolEnv("KIROCLI_GO_TOKEN_POOL_WARM_ON_START", true),
			TokenPoolRefreshInterval: durationEnv("KIROCLI_GO_TOKEN_POOL_REFRESH_INTERVAL_SEC", 30*time.Minute),
			TokenPoolStartupDelay:    durationEnv("KIROCLI_GO_TOKEN_POOL_STARTUP_DELAY_SEC", 3*time.Second),
		},
		State: StateConfig{
			PersistEnabled:  boolEnv("KIROCLI_GO_STATE_PERSIST_ENABLED", true),
			PersistInterval: durationEnv("KIROCLI_GO_STATE_PERSIST_INTERVAL_SEC", 60*time.Second),
			StatsPath:       envOrDefault("KIROCLI_GO_STATS_STATE_PATH", "data/stats_state.json"),
			CatalogPath:     envOrDefault("KIROCLI_GO_CATALOG_STATE_PATH", "data/catalog_state.json"),
		},
		Models: ModelsConfig{
			ThinkingSuffix: envOrDefault("KIROCLI_GO_THINKING_SUFFIX", "-thinking"),
		},
		Upstream: UpstreamConfig{
			CLIBaseURL:      envOrDefault("KIROCLI_GO_CLI_BASE_URL", "https://q.us-east-1.amazonaws.com/generateAssistantResponse"),
			CLIModelsURL:    envOrDefault("KIROCLI_GO_CLI_MODELS_URL", "https://q.us-east-1.amazonaws.com?origin=KIRO_CLI"),
			CLIMCPURL:       envOrDefault("KIROCLI_GO_CLI_MCP_URL", "https://q.us-east-1.amazonaws.com/mcp"),
			CLIProxyURL:     envOrDefault("KIROCLI_GO_PROXY_URL", ""),
			CLIUserAgent:    envOrDefault("KIROCLI_GO_CLI_USER_AGENT", "aws-sdk-rust/1.3.10 ua/2.1 api/codewhispererstreaming/0.1.12842 os/macos lang/rust/1.88.0 md/appVersion-1.23.1 app/AmazonQ-For-CLI"),
			CLIAmzUserAgent: envOrDefault("KIROCLI_GO_CLI_AMZ_USER_AGENT", "aws-sdk-rust/1.3.10 ua/2.1 api/codewhispererstreaming/0.1.12842 os/macos lang/rust/1.88.0 m/F app/AmazonQ-For-CLI"),
			CLIOrigin:       envOrDefault("KIROCLI_GO_CLI_ORIGIN", "KIRO_CLI"),
			CLITarget:       envOrDefault("KIROCLI_GO_CLI_TARGET", "AmazonCodeWhispererStreamingService.GenerateAssistantResponse"),
			CLIModelsTarget: envOrDefault("KIROCLI_GO_CLI_MODELS_TARGET", "AmazonCodeWhispererService.ListAvailableModels"),
			CLITimeout:      durationEnv("KIROCLI_GO_CLI_TIMEOUT_SEC", 5*time.Minute),
		},
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func durationEnv(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}

	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return fallback
	}

	return time.Duration(seconds) * time.Second
}

func intEnv(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}

	return value
}

func boolEnv(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
