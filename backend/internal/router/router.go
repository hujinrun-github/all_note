package router

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/handler"
	"github.com/hujinrun/flowspace/internal/middleware"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/transcription"
)

type Config struct {
	Store                    storage.Store
	ControlStore             storage.Store
	ProvisionControlIdentity func(context.Context, model.User) error
	Auth                     config.AuthConfig
	OAuthStateStore          auth.OAuthStateStore
	GitHubClient             handler.GitHubClient
	VoiceObjects             objectstore.Store
	Transcriber              transcription.Transcriber
	MaxVoiceBytes            int64
	MobileSyncV1Enabled      bool
	VoiceUploadEnabled       bool
	TranscriptionEnabled     bool
	WorkspaceSettings        handler.WorkspaceSettingsService
	CodexSubscription        handler.CodexSubscriptionService
	AIChat                   handler.WorkspaceChatService
	// Task-domain model-aware routing is intentionally opt-in. With both values
	// nil the existing legacy Web routes remain unchanged. Once configured, the
	// selector must prove legacy or v2 from durable workspace state on every
	// request; runtime failures never imply a legacy fallback. cmd/server wiring
	// remains a later deployment decision after cutover gates pass.
	TaskDomainV2Runtime     taskapp.RuntimeResolver
	TaskDomainModelSelector taskapp.ModelSelector
}

func Setup(cfg Config) *gin.Engine {
	repository.SetStore(cfg.Store)
	identityStore := cfg.ControlStore
	if identityStore == nil {
		identityStore = cfg.Store
	}

	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery(), middleware.CORS(cfg.Auth.AllowedOrigins))

	api := r.Group("/api")
	{
		api.GET("/health", handler.Health)

		authMiddleware := middleware.AuthMiddleware{Store: identityStore, WatchStore: cfg.Store, SessionSecret: cfg.Auth.SessionSecret, Cookie: cfg.Auth.Cookie}
		authRoutes := api.Group("/auth")
		authRoutes.POST("/login", handler.Login(identityStore, cfg.Auth))
		authRoutes.POST("/logout", authMiddleware.Optional(), handler.Logout(identityStore, cfg.Auth.Cookie))
		authRoutes.GET("/me", authMiddleware.Required(), handler.Me(identityStore))
		authRoutes.POST("/change-password", authMiddleware.Required(), handler.ChangePassword(identityStore))
		authRoutes.GET("/providers", handler.AuthProviders(cfg.Auth))
		authRoutes.GET("/github/start", handler.GitHubOAuthStart(identityStore, cfg.Auth, cfg.OAuthStateStore))
		authRoutes.GET("/github/callback", handler.GitHubOAuthCallbackAcrossStores(identityStore, cfg.Store, cfg.ProvisionControlIdentity, cfg.Auth, cfg.OAuthStateStore, cfg.GitHubClient))

		protected := api.Group("")
		protected.Use(authMiddleware.Required(), authMiddleware.RequirePasswordSettled())

		nativeProtected := api.Group("")
		nativeProtected.Use(authMiddleware.RequiredSessionOrWatch(), authMiddleware.RequirePasswordSettled())
		nativeProtected.POST("/voice-notes", handler.CreateVoiceNote(cfg.Store))
		nativeProtected.PUT("/voice-notes/:clientID/audio", handler.UploadVoiceAudio(cfg.Store, cfg.VoiceObjects, cfg.MaxVoiceBytes))
		nativeProtected.GET("/voice-notes/:clientID/audio", handler.GetVoiceAudio(cfg.Store, cfg.VoiceObjects))
		nativeProtected.GET("/voice-notes/:clientID/status", handler.GetVoiceNoteStatus(cfg.Store))
		nativeProtected.POST("/voice-notes/:clientID/transcription", handler.TranscribeVoiceNote(cfg.Store, cfg.VoiceObjects, cfg.Transcriber))

		watchRoutes := api.Group("/watch")
		watchRoutes.Use(authMiddleware.RequiredSessionOrWatch(), authMiddleware.RequirePasswordSettled())
		watchRoutes.GET("/snapshot", handler.GetWatchSnapshot(cfg.Store))
		watchRoutes.PATCH("/tasks/:id", handler.UpdateTask(cfg.Store))

		protected.GET("/folders", handler.GetFolders(cfg.Store))
		if cfg.MobileSyncV1Enabled {
			protected.GET("/mobile/capabilities", handler.GetMobileCapabilities(handler.MobileCapabilityFeatures{
				Sync: true, VoiceUpload: cfg.VoiceUploadEnabled,
				TranscriptionJobs: cfg.TranscriptionEnabled, WatchPairing: true,
			}))
			nativeProtected.GET("/mobile/sync/changes", handler.ListMobileChanges(cfg.Store, cfg.Auth.SessionSecret))
			nativeProtected.GET("/mobile/sync/snapshot", handler.GetMobileSnapshot(cfg.Store, cfg.Auth.SessionSecret))
			nativeProtected.POST("/mobile/sync/mutations", handler.ApplyMobileMutations(cfg.Store))
			protected.GET("/mobile/sync/conflicts", handler.ListMobileConflicts(cfg.Store))
			protected.POST("/mobile/sync/conflicts/:conflictID/resolve", handler.ResolveMobileConflict(cfg.Store))
			nativeProtected.PUT("/mobile/voice-notes/:clientID/audio", handler.UploadMobileVoiceAudio(cfg.Store, cfg.VoiceObjects, cfg.MaxVoiceBytes))
			protected.POST("/mobile/voice-notes/:clientID/transcriptions", handler.CreateMobileTranscriptionJob(cfg.Store))
			protected.GET("/mobile/transcription-jobs/:jobID", handler.GetMobileTranscriptionJob(cfg.Store))
			protected.POST("/mobile/transcription-jobs/:jobID/retry", handler.RetryMobileTranscriptionJob(cfg.Store))
		}
		protected.POST("/devices/watch/authorize", handler.AuthorizeWatchDevice(cfg.Store, cfg.Auth.SessionSecret))
		protected.POST("/devices/watch/revoke", handler.RevokeWatchDevice(cfg.Store))
		if cfg.Auth.EnableLocalDirectoryBrowser {
			systemAdmin := protected.Group("/system")
			systemAdmin.Use(authMiddleware.RequireAdmin())
			systemAdmin.GET("/directories", handler.ListLocalDirectories)
		}
		admin := protected.Group("/admin")
		admin.Use(authMiddleware.RequireAdmin())
		admin.GET("/users", handler.ListUsers(identityStore))
		admin.POST("/users", handler.CreateUserAcrossStores(identityStore, cfg.Store, cfg.ProvisionControlIdentity))
		admin.PATCH("/users/:id", handler.UpdateUser(identityStore))
		admin.POST("/users/:id/reset-password", handler.ResetUserPassword(identityStore))
		admin.POST("/users/:id/disable", handler.DisableUser(identityStore))
		admin.POST("/users/:id/enable", handler.EnableUser(identityStore))

		protected.GET("/settings/profile", handler.GetSettingsProfile(identityStore))
		protected.PATCH("/settings/profile", handler.UpdateSettingsProfile(identityStore))
		protected.GET("/settings/profile/avatar", handler.GetSettingsAvatar(identityStore))
		protected.PUT("/settings/profile/avatar", handler.PutSettingsAvatar(identityStore))
		protected.DELETE("/settings/profile/avatar", handler.DeleteSettingsAvatar(identityStore))
		protected.GET("/settings/runtime", handler.GetRuntimeSettings(cfg.WorkspaceSettings))
		protected.POST("/settings/profiles/test", handler.TestServiceProfile(cfg.WorkspaceSettings))
		protected.POST("/settings/profiles", handler.SaveServiceProfile(cfg.WorkspaceSettings))
		protected.POST("/settings/profiles/:kind/:versionID/verify", handler.VerifyServiceProfile(cfg.WorkspaceSettings))
		protected.PUT("/settings/bindings/:kind", handler.SetServiceBinding(cfg.WorkspaceSettings))
		protected.POST("/settings/ai/codex/device/start", handler.StartCodexSubscription(cfg.CodexSubscription))
		protected.POST("/settings/ai/codex/device/:flowID/poll", handler.PollCodexSubscription(cfg.CodexSubscription))

		protected.GET("/notes", handler.GetNotes(cfg.Store))
		protected.GET("/notes/:id", handler.GetNote(cfg.Store))
		protected.POST("/notes", handler.CreateNote(cfg.Store))
		protected.PATCH("/notes/:id", handler.UpdateNote(cfg.Store))
		protected.DELETE("/notes/:id", handler.DeleteNote(cfg.Store))
		protected.GET("/notes/:id/sync-binding", handler.GetNoteSyncBinding(cfg.Store))
		protected.PUT("/notes/:id/sync-binding", handler.PutNoteSyncBinding(cfg.Store))
		protected.DELETE("/notes/:id/sync-binding", handler.DeleteNoteSyncBinding(cfg.Store))
		protected.GET("/notes/:id/sync-state", handler.GetNoteSyncState(cfg.Store))

		protected.GET("/sync/targets", handler.ListSyncTargets(cfg.Store))
		protected.POST("/sync/targets", handler.SaveSyncTarget(cfg.Store))
		protected.PATCH("/sync/targets/:id", handler.UpdateSyncTarget(cfg.Store))
		protected.DELETE("/sync/targets/:id", handler.DeleteSyncTarget(cfg.Store))
		protected.POST("/sync/notes/:id", handler.SyncNote(cfg.Store))
		protected.POST("/sync/targets/:target_id/push", handler.SyncTargetPush(cfg.Store))
		protected.POST("/sync/targets/:target_id/pull", handler.SyncTargetPull(cfg.Store))
		protected.POST("/sync/targets/:target_id/bidirectional", handler.SyncTargetBidirectional(cfg.Store))
		protected.GET("/sync/targets/:target_id/deletions", handler.ListTargetDeletions(cfg.Store))
		protected.POST("/sync/targets/:target_id/deletions/:note_id/confirm", handler.ConfirmTargetDeletion(cfg.Store))
		protected.POST("/sync/targets/:target_id/deletions/:note_id/restore", handler.RestoreTargetDeletion(cfg.Store))
		protected.POST("/sync/obsidian/test", handler.TestObsidianTarget)
		protected.POST("/sync/obsidian/notes/:id", handler.SyncObsidianNote(cfg.Store))
		protected.POST("/sync/obsidian/folders/:folder_id", handler.SyncObsidianFolder(cfg.Store))
		protected.POST("/sync/obsidian/all", handler.SyncObsidianAll(cfg.Store))
		protected.POST("/sync/obsidian/pull", handler.SyncObsidianPull(cfg.Store))
		protected.POST("/sync/obsidian/bidirectional", handler.SyncObsidianBidirectional(cfg.Store))
		protected.GET("/sync/obsidian/deletions", handler.ListObsidianDeletions(cfg.Store))
		protected.POST("/sync/obsidian/deletions/:note_id/confirm", handler.ConfirmObsidianDeletion(cfg.Store))
		protected.POST("/sync/obsidian/deletions/:note_id/restore", handler.RestoreObsidianDeletion(cfg.Store))
		protected.POST("/sync/notion/test", handler.TestNotionTargetWithStore(cfg.Store))
		protected.POST("/sync/notion/all", handler.SyncNotionAll(cfg.Store))
		protected.POST("/sync/notion/pull", handler.SyncNotionPull(cfg.Store))
		protected.POST("/sync/notion/bidirectional", handler.SyncNotionBidirectional(cfg.Store))
		protected.POST("/sync/notion/notes/:id", handler.SyncNotionNote(cfg.Store))
		protected.GET("/sync/notion/deletions", handler.ListNotionDeletions(cfg.Store))
		protected.POST("/sync/notion/deletions/:note_id/confirm", handler.ConfirmNotionDeletion(cfg.Store))
		protected.POST("/sync/notion/deletions/:note_id/restore", handler.RestoreNotionDeletion(cfg.Store))

		registerTaskDomainRoutes(protected, cfg)

		protected.GET("/inbox", handler.GetInbox(cfg.Store))
		protected.POST("/inbox", handler.CreateInboxItem(cfg.Store))
		protected.POST("/inbox/:id/convert", handler.ConvertInboxItem(cfg.Store))
		protected.POST("/inbox/batch", handler.BatchInbox(cfg.Store))
		protected.DELETE("/inbox/:id", handler.DeleteInboxItem(cfg.Store))

		protected.GET("/search", handler.Search(cfg.Store))
		protected.POST("/japanese/furigana", handler.JapaneseFuriganaWithAI(cfg.AIChat))
	}

	return r
}
