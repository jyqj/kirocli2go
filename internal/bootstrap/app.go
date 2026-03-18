package bootstrap

import (
	"context"
	"errors"
	"log"
	"net/http"

	runtimecatalog "kirocli-go/internal/adapters/catalog/runtime"
	"kirocli-go/internal/adapters/catalog/static"
	anthropicformatter "kirocli-go/internal/adapters/formatter/anthropic"
	openaiformatter "kirocli-go/internal/adapters/formatter/openai"
	httpadmin "kirocli-go/internal/adapters/http/admin"
	httpstats "kirocli-go/internal/adapters/http/stats"
	"kirocli-go/internal/adapters/mcp/websearch"
	"kirocli-go/internal/adapters/token/provider"
	"kirocli-go/internal/adapters/upstream/clihttp"
	"kirocli-go/internal/application/chat"
	appstats "kirocli-go/internal/application/stats"
	"kirocli-go/internal/background"
	"kirocli-go/internal/config"
)

type App struct {
	server               *http.Server
	modelRefreshRunner   *background.ModelRefreshRunner
	tokenPoolRunner      *background.TokenPoolRunner
	statsPersistRunner   *background.StatePersistRunner[appstats.Snapshot]
	catalogPersistRunner *background.StatePersistRunner[runtimecatalog.Snapshot]
}

func NewApp(cfg config.Config) (*App, error) {
	tokenProvider, err := provider.New(provider.Config{
		Source:         cfg.Accounts.Source,
		BearerToken:    cfg.Accounts.BearerToken,
		CSVPath:        cfg.Accounts.CSVPath,
		APIURL:         cfg.Accounts.APIURL,
		APIToken:       cfg.Accounts.APIToken,
		APICategoryID:  cfg.Accounts.APICategoryID,
		APIFetchCount:  cfg.Accounts.APIFetchCount,
		ActivePoolSize: cfg.Accounts.ActivePoolSize,
		MaxRefreshTry:  cfg.Accounts.MaxRefreshTry,
		OIDCURL:        cfg.Accounts.OIDCURL,
		ProxyURL:       cfg.Upstream.CLIProxyURL,
		RefreshTimeout: cfg.Accounts.RefreshTimeout,
		StatePath:      cfg.Accounts.StatePath,
	})
	if err != nil {
		return nil, err
	}

	_ = static.New(static.Config{
		ThinkingSuffix: cfg.Models.ThinkingSuffix,
	})
	catalog := runtimecatalog.New(runtimecatalog.Config{
		ModelsURL:      cfg.Upstream.CLIModelsURL,
		ProxyURL:       cfg.Upstream.CLIProxyURL,
		UserAgent:      cfg.Upstream.CLIUserAgent,
		AmzUserAgent:   cfg.Upstream.CLIAmzUserAgent,
		Origin:         cfg.Upstream.CLIOrigin,
		ModelsTarget:   cfg.Upstream.CLIModelsTarget,
		Timeout:        cfg.Upstream.CLITimeout,
		ThinkingSuffix: cfg.Models.ThinkingSuffix,
	}, tokenProvider)
	statsCollector := appstats.NewCollector()
	requestLogs := appstats.NewRequestLogRing(500)
	statsPersistRunner := background.NewStatePersistRunner(
		cfg.State.PersistEnabled,
		cfg.State.PersistInterval,
		cfg.State.StatsPath,
		"stats",
		statsCollector,
	)
	if err := statsPersistRunner.Load(); err != nil {
		log.Printf("load stats snapshot failed: %v", err)
	}
	catalogPersistRunner := background.NewStatePersistRunner(
		cfg.State.PersistEnabled,
		cfg.State.PersistInterval,
		cfg.State.CatalogPath,
		"catalog",
		catalog,
	)
	if err := catalogPersistRunner.Load(); err != nil {
		log.Printf("load catalog snapshot failed: %v", err)
	}

	upstream := clihttp.New(clihttp.Config{
		BaseURL:      cfg.Upstream.CLIBaseURL,
		ProxyURL:     cfg.Upstream.CLIProxyURL,
		UserAgent:    cfg.Upstream.CLIUserAgent,
		AmzUserAgent: cfg.Upstream.CLIAmzUserAgent,
		Origin:       cfg.Upstream.CLIOrigin,
		Target:       cfg.Upstream.CLITarget,
		Timeout:      cfg.Upstream.CLITimeout,
	})

	chatService, err := chat.NewService(chat.Dependencies{
		Tokens:             tokenProvider,
		Upstream:           upstream,
		Catalog:            catalog,
		OpenAIFormatter:    openaiformatter.New(),
		AnthropicFormatter: anthropicformatter.New(),
		Stats:              statsCollector,
		RequestLogs:        requestLogs,
	})
	if err != nil {
		return nil, err
	}

	webSearchClient := websearch.New(websearch.Config{
		URL:      cfg.Upstream.CLIMCPURL,
		ProxyURL: cfg.Upstream.CLIProxyURL,
		Timeout:  cfg.Upstream.CLITimeout,
	}, tokenProvider)

	mux := NewMux(
		cfg,
		chatService,
		catalog,
		webSearchClient,
		httpstats.NewHandler(statsCollector),
		httpadmin.NewHandler(cfg, statsCollector, requestLogs, tokenProvider, catalog),
	)

	server := &http.Server{
		Addr:         cfg.Server.Address,
		Handler:      mux,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	return &App{
		server: server,
		modelRefreshRunner: background.NewModelRefreshRunner(
			cfg.Background.ModelRefreshEnabled,
			cfg.Background.ModelRefreshOnStart,
			cfg.Background.ModelRefreshStartupDelay,
			cfg.Background.ModelRefreshInterval,
			catalog,
		),
		tokenPoolRunner: background.NewTokenPoolRunner(
			cfg.Background.TokenPoolEnabled,
			cfg.Background.TokenPoolWarmOnStart,
			cfg.Background.TokenPoolStartupDelay,
			cfg.Background.TokenPoolRefreshInterval,
			tokenProvider,
		),
		statsPersistRunner:   statsPersistRunner,
		catalogPersistRunner: catalogPersistRunner,
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	errCh := make(chan error, 1)

	if a.modelRefreshRunner != nil {
		a.modelRefreshRunner.Start(ctx)
	}
	if a.tokenPoolRunner != nil {
		a.tokenPoolRunner.Start(ctx)
	}
	if a.statsPersistRunner != nil {
		a.statsPersistRunner.Start(ctx)
	}
	if a.catalogPersistRunner != nil {
		a.catalogPersistRunner.Start(ctx)
	}

	go func() {
		log.Printf("kirocli-go skeleton listening on %s", a.server.Addr)
		if err := a.server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		return a.server.Shutdown(context.Background())
	case err := <-errCh:
		return err
	}
}
