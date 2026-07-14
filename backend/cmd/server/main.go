package main

import (
	"context"
	"log"
	"time"

	authpkg "github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/bootstrap"
	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/handler"
	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/router"
	storagepkg "github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/storage/postgres"
	"github.com/hujinrun/flowspace/internal/storage/sqlite"
	"github.com/hujinrun/flowspace/internal/transcription"
	"github.com/hujinrun/flowspace/internal/transcriptionjob"
)

func main() {
	runtimeConfig := config.LoadStorageConfig()
	storageConfig := storagepkg.LoadStorageConfig(runtimeConfig.Environment)

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

	server := config.LoadServerConfig(runtimeConfig.Environment)
	oauthStateStore := authpkg.NewMemoryOAuthStateStore()
	oauthStateCtx, stopOAuthStateCleanup := context.WithCancel(context.Background())
	defer stopOAuthStateCleanup()
	go oauthStateStore.RunCleanup(oauthStateCtx, 2*time.Minute, 1000)

	r := router.Setup(router.Config{
		Store:               store,
		Auth:                authCfg,
		OAuthStateStore:     oauthStateStore,
		GitHubClient:        handler.NewGitHubHTTPClient(authCfg.GitHub),
		VoiceObjects:        voiceObjects,
		Transcriber:         voiceTranscriber,
		MaxVoiceBytes:       nativeCfg.MaxVoiceAudioBytes,
		MobileSyncV1Enabled: nativeCfg.MobileSyncV1Enabled,
	})
	if nativeCfg.MobileSyncV1Enabled && nativeCfg.MinIO.Enabled() && nativeCfg.Transcription.Enabled() {
		workerCtx, stopWorker := context.WithCancel(context.Background())
		defer stopWorker()
		worker := transcriptionjob.NewWorker(store, voiceObjects, voiceTranscriber, "server-transcription-worker")
		go worker.Run(workerCtx, time.Second, func(err error) {
			log.Printf("transcription worker: %v", err)
		})
		log.Printf("durable transcription worker initialized")
	}
	addr := ":" + server.Port
	log.Printf("server starting on %s", addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
