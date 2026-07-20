package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/hujinrun/flowspace/internal/airuntime"
	authpkg "github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/bootstrap"
	"github.com/hujinrun/flowspace/internal/codexoauth"
	"github.com/hujinrun/flowspace/internal/codexsubscription"
	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/controlprofile"
	"github.com/hujinrun/flowspace/internal/controlsettings"
	"github.com/hujinrun/flowspace/internal/credentials"
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
	"github.com/hujinrun/flowspace/internal/transcription"
	"github.com/hujinrun/flowspace/internal/transcriptionjob"
	"github.com/hujinrun/flowspace/internal/voiceaudiocleanup"
)

func main() {
	legacyConfig := config.LoadStorageConfig()
	runtimeConfig, err := config.LoadRuntimeStorageConfig(legacyConfig.Environment, config.RuntimeStorageLoadOptions{AllowLegacyUpgrade: true})
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
	var voiceObjects objectstore.Store = objectstore.UnavailableStore{}
	if nativeCfg.MinIO.Enabled() {
		voiceObjects, err = objectstore.NewMinIOStore(startupCtx, nativeCfg.MinIO)
		if err != nil {
			log.Fatalf("voice object storage: %v", err)
		}
		log.Printf("voice object storage initialized bucket=%s", nativeCfg.MinIO.Bucket)
	} else {
		log.Printf("voice object storage disabled: FLOWSPACE_MINIO_ENDPOINT is not configured")
	}
	var voiceTranscriber transcription.Transcriber
	if nativeCfg.Transcription.Enabled() {
		voiceTranscriber = transcription.NewHTTPTranscriber(nativeCfg.Transcription)
		log.Printf("voice transcription initialized")
	} else {
		log.Printf("voice transcription disabled: FLOWSPACE_TRANSCRIPTION_URL is not configured")
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

	workspaceSettings, codexSubscription, aiChat, runtimeTranscriber, controlStore, err := openWorkspaceSettings(startupCtx, registry, runtimeConfig, store)
	if err != nil {
		log.Fatalf("control plane: %v", err)
	}
	defer controlStore.Close()
	voiceTranscriber = runtimeTranscriber
	server := config.LoadServerConfig(runtimeConfig.Environment)
	oauthStateStore := authpkg.NewMemoryOAuthStateStore()
	oauthStateCtx, stopOAuthStateCleanup := context.WithCancel(context.Background())
	defer stopOAuthStateCleanup()
	go oauthStateStore.RunCleanup(oauthStateCtx, 2*time.Minute, 1000)

	r := router.Setup(router.Config{
		Store:                store,
		Auth:                 authCfg,
		OAuthStateStore:      oauthStateStore,
		GitHubClient:         handler.NewGitHubHTTPClient(authCfg.GitHub),
		VoiceObjects:         voiceObjects,
		Transcriber:          voiceTranscriber,
		MaxVoiceBytes:        nativeCfg.MaxVoiceAudioBytes,
		MobileSyncV1Enabled:  nativeCfg.MobileSyncV1Enabled,
		VoiceUploadEnabled:   nativeCfg.MobileSyncV1Enabled && nativeCfg.MinIO.Enabled(),
		TranscriptionEnabled: nativeCfg.MobileSyncV1Enabled && nativeCfg.MinIO.Enabled() && nativeCfg.Transcription.Enabled(),
		WorkspaceSettings:    workspaceSettings,
		CodexSubscription:    codexSubscription,
		AIChat:               aiChat,
	})
	if nativeCfg.MobileSyncV1Enabled && nativeCfg.MinIO.Enabled() {
		workerCtx, stopWorker := context.WithCancel(context.Background())
		defer stopWorker()
		worker := transcriptionjob.NewWorker(store, voiceObjects, voiceTranscriber, "server-transcription-worker")
		go worker.Run(workerCtx, time.Second, func(err error) {
			log.Printf("transcription worker: %v", err)
		})
		log.Printf("durable transcription worker initialized")
	}
	if nativeCfg.MobileSyncV1Enabled && nativeCfg.MinIO.Enabled() {
		cleanupCtx, stopCleanup := context.WithCancel(context.Background())
		defer stopCleanup()
		worker := voiceaudiocleanup.NewWorker(store, voiceObjects, "server-voice-audio-cleanup-worker")
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
	addr := ":" + server.Port
	log.Printf("server starting on %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

func databaseStorageConfig(environment string, cfg config.DatabaseConfig) storagepkg.Config {
	return storagepkg.Config{Env: environment, Driver: storagepkg.Driver(cfg.Driver), URL: cfg.URL, SQLitePath: cfg.SQLitePath}
}

func openWorkspaceSettings(ctx context.Context, registry *storagepkg.Registry, cfg config.RuntimeStorageConfig, tenant storagepkg.Store) (*controlsettings.Service, *codexsubscription.Service, *airuntime.Generator, transcription.Transcriber, storagepkg.Store, error) {
	controlStore, err := registry.OpenControl(ctx, databaseStorageConfig(cfg.Environment, cfg.Control))
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	fail := func(err error) (*controlsettings.Service, *codexsubscription.Service, *airuntime.Generator, transcription.Transcriber, storagepkg.Store, error) {
		_ = controlStore.Close()
		return nil, nil, nil, nil, nil, err
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
	users, _, err := tenant.Auth().ListUsers(ctx, storagepkg.UserListFilter{Page: 1, PageSize: 1000})
	if err != nil {
		return fail(err)
	}
	postgresDialect := cfg.Control.Driver == config.DatabaseDriverPostgres
	for _, user := range users {
		if user.DefaultWorkspaceID != "" {
			if err := controlsettings.ProvisionWorkspaceIdentity(ctx, sqlStore.SQLDB(), postgresDialect, user); err != nil {
				return fail(err)
			}
		}
	}
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
	runtimeRepository, err := runtimecontrol.New(sqlStore.SQLDB(), runtimeDialect)
	if err != nil {
		return fail(err)
	}
	authorizer, err := controlsettings.NewSQLAuthorizer(sqlStore.SQLDB(), postgresDialect)
	if err != nil {
		return fail(err)
	}
	prober, err := controlsettings.NewHTTPProber()
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
	dialer, err := outbound.NewDialer(nil, outbound.Policy{})
	if err != nil {
		return fail(err)
	}
	oauthClient := codexoauth.NewClient(dialer.HTTPClient(), codexoauth.DefaultIssuer, codexoauth.DefaultTokenURL)
	codexService, err := codexsubscription.New(oauthClient, flows, profiles, authorizer)
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
	runtimeTranscriber, err := transcription.NewRuntimeTranscriber(aiResolver, dialer.HTTPClient())
	if err != nil {
		return fail(err)
	}
	return service, codexService, aiGenerator, runtimeTranscriber, controlStore, nil
}
