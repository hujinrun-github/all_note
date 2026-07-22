package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/airuntime"
	authpkg "github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/bootstrap"
	"github.com/hujinrun/flowspace/internal/codexoauth"
	"github.com/hujinrun/flowspace/internal/codexsubscription"
	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/controlprofile"
	"github.com/hujinrun/flowspace/internal/controlsettings"
	"github.com/hujinrun/flowspace/internal/credentials"
	"github.com/hujinrun/flowspace/internal/generationclaims"
	"github.com/hujinrun/flowspace/internal/handler"
	"github.com/hujinrun/flowspace/internal/mobilesyncpublisher"
	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/outbound"
	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/router"
	"github.com/hujinrun/flowspace/internal/runtimecontrol"
	storagepkg "github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/postgres"
	"github.com/hujinrun/flowspace/internal/storage/sqlite"
	"github.com/hujinrun/flowspace/internal/taskgeneration"
	"github.com/hujinrun/flowspace/internal/taskruntime"
	"github.com/hujinrun/flowspace/internal/tenantruntime"
	"github.com/hujinrun/flowspace/internal/transcription"
	"github.com/hujinrun/flowspace/internal/transcriptionjob"
	"github.com/hujinrun/flowspace/internal/voiceaudiocleanup"
)

func main() {
	legacyConfig := config.LoadStorageConfig()
	runtimeConfig, err := config.LoadRuntimeStorageConfig(legacyConfig.Environment, config.RuntimeStorageLoadOptions{AllowLegacyUpgrade: false})
	if err != nil {
		log.Fatalf("runtime storage config: %v", err)
	}
	storageConfig := databaseStorageConfig(runtimeConfig.Environment, runtimeConfig.PlatformData)

	registry := storagepkg.NewRegistry()
	if err := registry.Register(postgres.Provider{}); err != nil {
		log.Fatalf("register postgres provider: %v", err)
	}
	if err := registry.Register(sqlite.Provider{}); err != nil {
		log.Fatalf("register sqlite provider: %v", err)
	}

	startupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := registry.Open(startupCtx, storageConfig)
	if err != nil {
		log.Fatalf("open storage provider: %v", err)
	}
	defer store.Close()

	authCfg, err := config.LoadAuthConfig(runtimeConfig.Environment)
	if err != nil {
		log.Fatalf("auth config: %v", err)
	}
	nativeCfg, err := config.LoadNativeConfig()
	if err != nil {
		log.Fatalf("native app config: %v", err)
	}
	bootstrapCfg := bootstrap.Config{
		AdminEmail:    authCfg.Bootstrap.Email,
		AdminPassword: authCfg.Bootstrap.Password,
		AdminName:     authCfg.Bootstrap.Name,
	}
	if err := bootstrap.EnsureAuthReady(startupCtx, store, bootstrapCfg); err != nil {
		log.Fatalf("auth bootstrap: %v", err)
	}
	if finalizer, ok := store.(interface {
		FinalizeAuthSchema(context.Context) error
	}); ok {
		state, err := bootstrap.InspectState(startupCtx, store)
		if err != nil {
			log.Fatalf("auth bootstrap state: %v", err)
		}
		if state.HasUsers {
			if err := finalizer.FinalizeAuthSchema(startupCtx); err != nil {
				log.Fatalf("auth schema finalizer: %v", err)
			}
		}
	}

	repository.SetStore(store)
	log.Printf("storage initialized env=%s driver=%s database=%s sqlite_path=%s capabilities=%+v", storageConfig.Env, storageConfig.Driver, storageConfig.Name, storageConfig.SQLitePath, store.Capabilities())

	workspaceSettings, codexSubscription, aiChat, runtimeTranscriber, runtimeObjects, taskDomainRuntime, controlStore, identityProvisioner, err := openWorkspaceSettings(startupCtx, registry, runtimeConfig, nativeCfg, store)
	if err != nil {
		log.Fatalf("control plane: %v", err)
	}
	defer closeTaskDomainAndControl(taskDomainRuntime, controlStore)
	serverCfg := config.LoadServerConfig(runtimeConfig.Environment)
	oauthStateStore := authpkg.NewMemoryOAuthStateStore()
	oauthStateCtx, stopOAuthStateCleanup := context.WithCancel(context.Background())
	defer stopOAuthStateCleanup()
	go oauthStateStore.RunCleanup(oauthStateCtx, 2*time.Minute, 1000)

	routerConfig := router.Config{
		Store:                    store,
		ControlStore:             controlStore,
		ProvisionControlIdentity: identityProvisioner.Provision,
		Auth:                     authCfg,
		OAuthStateStore:          oauthStateStore,
		GitHubClient:             handler.NewGitHubHTTPClient(authCfg.GitHub),
		VoiceObjects:             runtimeObjects,
		Transcriber:              runtimeTranscriber,
		MaxVoiceBytes:            nativeCfg.MaxVoiceAudioBytes,
		MobileSyncV1Enabled:      nativeCfg.MobileSyncV1Enabled,
		VoiceUploadEnabled:       nativeCfg.MobileSyncV1Enabled,
		TranscriptionEnabled:     nativeCfg.MobileSyncV1Enabled,
		WorkspaceSettings:        workspaceSettings,
		CodexSubscription:        codexSubscription,
		AIChat:                   aiChat,
	}
	if taskDomainRuntime != nil {
		routerConfig.TaskDomainV2Runtime = taskDomainRuntime.application
		routerConfig.TaskDomainModelSelector = taskDomainRuntime.models
		log.Printf("task-domain model-aware routing initialized")
	}
	r := router.Setup(routerConfig)
	if nativeCfg.MobileSyncV1Enabled {
		workerCtx, stopWorker := context.WithCancel(context.Background())
		defer stopWorker()
		worker := transcriptionjob.NewWorker(store, runtimeObjects, runtimeTranscriber, "server-transcription-worker")
		go worker.Run(workerCtx, time.Second, func(err error) {
			log.Printf("transcription worker: %v", err)
		})
		log.Printf("durable transcription worker initialized")
	}
	if nativeCfg.MobileSyncV1Enabled {
		cleanupCtx, stopCleanup := context.WithCancel(context.Background())
		defer stopCleanup()
		worker := voiceaudiocleanup.NewWorker(store, runtimeObjects, "server-voice-audio-cleanup-worker")
		go worker.Run(cleanupCtx, time.Second, func(err error) {
			log.Printf("voice audio cleanup worker: %v", err)
		})
		log.Printf("durable voice audio cleanup worker initialized")
	}
	if nativeCfg.MobileSyncV1Enabled {
		publisherCtx, stopPublisher := context.WithCancel(context.Background())
		defer stopPublisher()
		worker := mobilesyncpublisher.NewWorker(store)
		go worker.Run(publisherCtx, 250*time.Millisecond, func(err error) {
			log.Printf("mobile sync publisher worker: %v", err)
		})
		log.Printf("mobile sync publisher worker initialized")
	}
	addr := ":" + serverCfg.Port
	log.Printf("server starting on %s", addr)
	httpServer := &http.Server{Addr: addr, Handler: r}
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- httpServer.ListenAndServe()
	}()
	shutdownSignal, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	select {
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			// Return through deferred lifecycle cleanup so generation drains before
			// tenant resolver and control-store shutdown.
			log.Printf("server failed: %v", err)
		}
	case <-shutdownSignal.Done():
		log.Printf("server shutdown requested")
		shutdownCtx, stopShutdown := context.WithTimeout(context.Background(), 15*time.Second)
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("server shutdown: %v", err)
		}
		stopShutdown()
		if err := <-serverErrors; err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server stopped: %v", err)
		}
	}
}

func databaseStorageConfig(environment string, cfg config.DatabaseConfig) storagepkg.Config {
	return storagepkg.Config{Env: environment, Driver: storagepkg.Driver(cfg.Driver), URL: cfg.URL, SQLitePath: cfg.SQLitePath}
}

type taskDomainRuntimeBundle struct {
	application *taskruntime.Resolver
	models      *taskruntime.DurableModelSelector
	tenants     interface{ Close() error }
	generation  interface{ Close() error }
}

func (bundle *taskDomainRuntimeBundle) Close() error {
	if bundle == nil {
		return nil
	}
	// Stop and drain generation before closing tenant resources. The daemon can
	// hold a fenced tenant transaction while a batch is running.
	var generationErr, tenantErr error
	if bundle.generation != nil {
		generationErr = bundle.generation.Close()
	}
	if bundle.tenants != nil {
		tenantErr = bundle.tenants.Close()
	}
	return errors.Join(generationErr, tenantErr)
}

func closeTaskDomainAndControl(bundle *taskDomainRuntimeBundle, control interface{ Close() error }) error {
	var runtimeErr, controlErr error
	if bundle != nil {
		runtimeErr = bundle.Close()
	}
	if control != nil {
		controlErr = control.Close()
	}
	return errors.Join(runtimeErr, controlErr)
}

func openWorkspaceSettings(ctx context.Context, registry *storagepkg.Registry, cfg config.RuntimeStorageConfig, nativeCfg config.NativeConfig, tenant storagepkg.Store) (*controlsettings.Service, *codexsubscription.Service, *airuntime.Generator, transcription.Transcriber, objectstore.Store, *taskDomainRuntimeBundle, storagepkg.Store, *controlsettings.IdentityProvisioner, error) {
	controlStore, err := registry.OpenControl(ctx, databaseStorageConfig(cfg.Environment, cfg.Control))
	if err != nil {
		return nil, nil, nil, nil, nil, nil, nil, nil, err
	}
	fail := func(err error) (*controlsettings.Service, *codexsubscription.Service, *airuntime.Generator, transcription.Transcriber, objectstore.Store, *taskDomainRuntimeBundle, storagepkg.Store, *controlsettings.IdentityProvisioner, error) {
		_ = controlStore.Close()
		return nil, nil, nil, nil, nil, nil, nil, nil, err
	}
	sqlStore, ok := controlStore.(storagepkg.SQLStore)
	if !ok {
		return fail(fmt.Errorf("control store does not expose SQL database"))
	}
	credentialsConfig, err := config.LoadCredentialsConfig()
	if err != nil {
		return fail(err)
	}
	keyring, err := credentials.LoadKeyringFile(credentialsConfig.KeyringFile, credentialsConfig.ActiveKeyID)
	if err != nil {
		return fail(err)
	}
	postgresDialect := cfg.Control.Driver == config.DatabaseDriverPostgres
	profileDialect := controlprofile.DialectSQLite
	runtimeDialect := runtimecontrol.DialectSQLite
	if postgresDialect {
		profileDialect = controlprofile.DialectPostgres
		runtimeDialect = runtimecontrol.DialectPostgres
	}
	profiles, err := controlprofile.New(sqlStore.SQLDB(), profileDialect, keyring)
	if err != nil {
		return fail(err)
	}
	defaultBindings, err := controlsettings.ReconcileSystemDefaults(ctx, profiles, platformSystemProfileSpecs(cfg.PlatformData, nativeCfg))
	if err != nil {
		return fail(err)
	}
	identityProvisioner, err := controlsettings.NewIdentityProvisioner(sqlStore.SQLDB(), postgresDialect, defaultBindings)
	if err != nil {
		return fail(err)
	}
	for page := 1; ; page++ {
		users, _, err := tenant.Auth().ListUsers(ctx, storagepkg.UserListFilter{Page: page, PageSize: 250})
		if err != nil {
			return fail(err)
		}
		for _, user := range users {
			if user.DefaultWorkspaceID == "" {
				continue
			}
			if err := controlsettings.ProvisionWorkspaceIdentity(ctx, sqlStore.SQLDB(), postgresDialect, user, defaultBindings); err != nil {
				return fail(err)
			}
			if err := projectLegacyUserMetadata(ctx, tenant, controlStore, user.ID); err != nil {
				return fail(err)
			}
		}
		if len(users) < 250 {
			break
		}
	}
	runtimeRepository, err := runtimecontrol.New(sqlStore.SQLDB(), runtimeDialect)
	if err != nil {
		return fail(err)
	}
	authorizer, err := controlsettings.NewSQLAuthorizer(sqlStore.SQLDB(), postgresDialect)
	if err != nil {
		return fail(err)
	}
	outboundPolicy, err := config.LoadOutboundPolicy()
	if err != nil {
		return fail(err)
	}
	prober, err := controlsettings.NewHTTPProber(outboundPolicy)
	if err != nil {
		return fail(err)
	}
	service, err := controlsettings.New(profiles, runtimeRepository, authorizer, prober)
	if err != nil {
		return fail(err)
	}
	oauthDialect := codexoauth.DialectSQLite
	if postgresDialect {
		oauthDialect = codexoauth.DialectPostgres
	}
	flows, err := codexoauth.NewRepository(sqlStore.SQLDB(), oauthDialect, keyring)
	if err != nil {
		return fail(err)
	}
	dialer, err := outbound.NewDialer(nil, outboundPolicy)
	if err != nil {
		return fail(err)
	}
	oauthClient := codexoauth.NewClient(dialer.HTTPClient(), codexoauth.DefaultIssuer, codexoauth.DefaultTokenURL)
	codexService, err := codexsubscription.New(oauthClient, flows, profiles, runtimeRepository, authorizer)
	if err != nil {
		return fail(err)
	}
	aiDialect := airuntime.ControlSQLite
	if postgresDialect {
		aiDialect = airuntime.ControlPostgres
	}
	aiSource, err := airuntime.NewControlSource(sqlStore.SQLDB(), aiDialect, keyring)
	if err != nil {
		return fail(err)
	}
	aiResolver, err := airuntime.NewResolver(aiSource)
	if err != nil {
		return fail(err)
	}
	aiGenerator, err := airuntime.NewGenerator(aiResolver, dialer.HTTPClient())
	if err != nil {
		return fail(err)
	}
	aiGenerator.SetCodexCredentialRefresher(codexService)
	runtimeTranscriber, err := transcription.NewRuntimeTranscriber(aiResolver, dialer.HTTPClient())
	if err != nil {
		return fail(err)
	}
	objectFactory, err := objectstore.NewMinIORuntimeFactory(dialer.HTTPClient())
	if err != nil {
		return fail(err)
	}
	runtimeObjects, err := objectstore.NewRuntimeStore(aiSource, objectFactory)
	if err != nil {
		return fail(err)
	}
	var taskDomainRuntime *taskDomainRuntimeBundle
	if nativeCfg.TaskDomainV2RoutingEnabled {
		controlRuntimeSource, err := tenantruntime.NewControlSource(sqlStore.SQLDB(), tenantruntime.ControlDialect(runtimeDialect))
		if err != nil {
			return fail(err)
		}
		endpointSource, err := taskruntime.NewProfileDatabaseEndpointConfigSource(aiSource, cfg.Environment)
		if err != nil {
			return fail(err)
		}
		nudgeRelay := taskgeneration.NewNudgeRelay()
		factory, err := taskruntime.NewFactory(endpointSource, taskruntime.ExpectedTenantSchemaVersion, dialer.DialContext,
			taskruntime.WithGenerationNudger(nudgeRelay))
		if err != nil {
			return fail(err)
		}
		tenants, err := tenantruntime.NewResolver(controlRuntimeSource, factory)
		if err != nil {
			return fail(err)
		}
		application, err := taskruntime.NewResolver(tenants)
		if err != nil {
			_ = tenants.Close()
			return fail(err)
		}
		models, err := taskruntime.NewModelSelector(tenants)
		if err != nil {
			_ = tenants.Close()
			return fail(err)
		}
		claimDialect := generationclaims.DialectSQLite
		generationControlDialect := taskgeneration.ControlDialectSQLite
		if postgresDialect {
			claimDialect = generationclaims.DialectPostgres
			generationControlDialect = taskgeneration.ControlDialectPostgres
		}
		claims, err := generationclaims.New(sqlStore.SQLDB(), claimDialect)
		if err != nil {
			_ = tenants.Close()
			return fail(err)
		}
		generation, err := taskgeneration.NewProductionDaemon(sqlStore.SQLDB(), generationControlDialect, claims, application,
			taskgeneration.ProductionConfig{
				SchedulerInterval:  15 * time.Minute,
				WorkerPollInterval: time.Second,
				BatchSize:          16,
				OnError: func(err error) {
					log.Printf("task-domain generation: %v", err)
				},
			})
		if err != nil {
			_ = tenants.Close()
			return fail(err)
		}
		if err := nudgeRelay.Bind(generation); err != nil {
			_ = generation.Close()
			_ = tenants.Close()
			return fail(err)
		}
		// startupCtx is intentionally not used: it has a 30-second deadline and
		// would silently stop the production worker after startup.
		if err := generation.Start(context.Background()); err != nil {
			_ = generation.Close()
			_ = tenants.Close()
			return fail(err)
		}
		taskDomainRuntime = &taskDomainRuntimeBundle{
			application: application, models: models, tenants: tenants, generation: generation,
		}
	}
	return service, codexService, aiGenerator, runtimeTranscriber, runtimeObjects, taskDomainRuntime, controlStore, identityProvisioner, nil
}

func projectLegacyUserMetadata(ctx context.Context, tenant, control storagepkg.Store, userID string) error {
	if _, err := control.Auth().GetUserProfile(ctx, userID); errors.Is(err, sql.ErrNoRows) {
		profile, sourceErr := tenant.Auth().GetUserProfile(ctx, userID)
		if sourceErr == nil {
			if err := control.Auth().UpsertUserProfile(ctx, profile); err != nil {
				return err
			}
		} else if !errors.Is(sourceErr, sql.ErrNoRows) {
			return sourceErr
		}
	} else if err != nil {
		return err
	}
	if _, err := control.Auth().GetUserAvatar(ctx, userID); errors.Is(err, sql.ErrNoRows) {
		avatar, sourceErr := tenant.Auth().GetUserAvatar(ctx, userID)
		if sourceErr == nil {
			if err := control.Auth().UpsertUserAvatar(ctx, avatar); err != nil {
				return err
			}
		} else if !errors.Is(sourceErr, sql.ErrNoRows) {
			return sourceErr
		}
	} else if err != nil {
		return err
	}
	identities, err := tenant.Auth().ListAuthIdentitiesByUser(ctx, userID)
	if err != nil {
		return err
	}
	for index := range identities {
		identity := identities[index]
		if _, err := control.Auth().GetAuthIdentity(ctx, identity.Provider, identity.ProviderUserID); errors.Is(err, sql.ErrNoRows) {
			if err := control.Auth().CreateAuthIdentity(ctx, &identity); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	return nil
}

func platformSystemProfileSpecs(data config.DatabaseConfig, nativeCfg config.NativeConfig) []controlsettings.SystemProfileSpec {
	dataConfig := map[string]any{"driver": string(data.Driver), "schema": "public"}
	var dataSecret []byte
	if data.Driver == config.DatabaseDriverSQLite {
		dataConfig["path"] = data.SQLitePath
		dataConfig["schema"] = "main"
	} else if parsed, err := url.Parse(data.URL); err == nil {
		if parsed.User != nil {
			if password, ok := parsed.User.Password(); ok {
				dataSecret = []byte(password)
			}
			parsed.User = url.User(parsed.User.Username())
		}
		dataConfig["endpoint"] = parsed.String()
	}
	dataJSON, _ := json.Marshal(dataConfig)

	objectProvider, objectMode := "unavailable", "default"
	objectConfig := map[string]any{"reason": "not_configured"}
	var objectSecret []byte
	if nativeCfg.MinIO.Enabled() {
		objectProvider = "minio"
		scheme := "http"
		if nativeCfg.MinIO.UseSSL {
			scheme = "https"
		}
		objectConfig = map[string]any{"endpoint": scheme + "://" + nativeCfg.MinIO.Endpoint, "bucket": nativeCfg.MinIO.Bucket, "region": nativeCfg.MinIO.Region}
		objectSecret, _ = json.Marshal(map[string]string{"access_key": nativeCfg.MinIO.AccessKey, "secret_key": nativeCfg.MinIO.SecretKey})
	}
	objectJSON, _ := json.Marshal(objectConfig)

	chatProvider, chatMode := "unavailable", "disabled"
	chatConfig := map[string]any{"reason": "not_configured"}
	var chatSecret []byte
	if key := strings.TrimSpace(os.Getenv("AI_API_KEY")); key != "" && strings.ToLower(strings.TrimSpace(os.Getenv("AI_PROVIDER"))) != "none" {
		chatProvider, chatMode, chatSecret = "openai_compatible", "default", []byte(key)
		endpoint := strings.TrimRight(strings.TrimSpace(os.Getenv("AI_BASE_URL")), "/")
		if endpoint == "" {
			endpoint = "https://api.deepseek.com"
		}
		model := strings.TrimSpace(os.Getenv("AI_MODEL"))
		if model == "" {
			model = "deepseek-v4-pro"
		}
		chatConfig = map[string]any{"endpoint": endpoint, "model": model}
	}
	chatJSON, _ := json.Marshal(chatConfig)

	transcriptionProvider, transcriptionMode := "unavailable", "disabled"
	transcriptionConfig := map[string]any{"reason": "not_configured"}
	var transcriptionSecret []byte
	if nativeCfg.Transcription.Enabled() {
		transcriptionProvider, transcriptionMode = nativeCfg.Transcription.Provider, "default"
		transcriptionConfig = map[string]any{"endpoint": nativeCfg.Transcription.URL, "model": nativeCfg.Transcription.Model}
		transcriptionSecret = []byte(nativeCfg.Transcription.APIKey)
	}
	transcriptionJSON, _ := json.Marshal(transcriptionConfig)

	return []controlsettings.SystemProfileSpec{
		{CandidateID: uuid.NewString(), FamilyID: "platform-data", Kind: "data_store", Name: "Platform database", Provider: string(data.Driver), ConfigJSON: string(dataJSON), Secret: dataSecret, Mode: "default"},
		{CandidateID: uuid.NewString(), FamilyID: "platform-objects", Kind: "object_s3", Name: "Platform object storage", Provider: objectProvider, ConfigJSON: string(objectJSON), Secret: objectSecret, Mode: objectMode},
		{CandidateID: uuid.NewString(), FamilyID: "platform-chat", Kind: "llm_chat", Name: "Platform text AI", Provider: chatProvider, ConfigJSON: string(chatJSON), Secret: chatSecret, Mode: chatMode},
		{CandidateID: uuid.NewString(), FamilyID: "platform-transcription", Kind: "llm_transcription", Name: "Platform transcription", Provider: transcriptionProvider, ConfigJSON: string(transcriptionJSON), Secret: transcriptionSecret, Mode: transcriptionMode},
	}
}
