package router

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/handler"
	"github.com/hujinrun/flowspace/internal/middleware"
	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/transcription"
)

type Config struct {
	Store           storage.Store
	Auth            config.AuthConfig
	OAuthStateStore auth.OAuthStateStore
	GitHubClient    handler.GitHubClient
	VoiceObjects    objectstore.Store
	Transcriber     transcription.Transcriber
	MaxVoiceBytes   int64
}

func Setup(cfg Config) *gin.Engine {
	repository.SetStore(cfg.Store)

	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery(), middleware.CORS(cfg.Auth.AllowedOrigins))

	api := r.Group("/api")
	{
		api.GET("/health", handler.Health)

		authMiddleware := middleware.AuthMiddleware{Store: cfg.Store, SessionSecret: cfg.Auth.SessionSecret, Cookie: cfg.Auth.Cookie}
		authRoutes := api.Group("/auth")
		authRoutes.POST("/login", handler.Login(cfg.Store, cfg.Auth))
		authRoutes.POST("/logout", authMiddleware.Optional(), handler.Logout(cfg.Store, cfg.Auth.Cookie))
		authRoutes.GET("/me", authMiddleware.Required(), handler.Me(cfg.Store))
		authRoutes.POST("/change-password", authMiddleware.Required(), handler.ChangePassword(cfg.Store))
		authRoutes.GET("/providers", handler.AuthProviders(cfg.Auth))
		authRoutes.GET("/github/start", handler.GitHubOAuthStart(cfg.Store, cfg.Auth, cfg.OAuthStateStore))
		authRoutes.GET("/github/callback", handler.GitHubOAuthCallback(cfg.Store, cfg.Auth, cfg.OAuthStateStore, cfg.GitHubClient))

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
		protected.POST("/devices/watch/authorize", handler.AuthorizeWatchDevice(cfg.Store, cfg.Auth.SessionSecret))
		protected.POST("/devices/watch/revoke", handler.RevokeWatchDevice(cfg.Store))
		if cfg.Auth.EnableLocalDirectoryBrowser {
			systemAdmin := protected.Group("/system")
			systemAdmin.Use(authMiddleware.RequireAdmin())
			systemAdmin.GET("/directories", handler.ListLocalDirectories)
		}
		admin := protected.Group("/admin")
		admin.Use(authMiddleware.RequireAdmin())
		admin.GET("/users", handler.ListUsers(cfg.Store))
		admin.POST("/users", handler.CreateUser(cfg.Store))
		admin.PATCH("/users/:id", handler.UpdateUser(cfg.Store))
		admin.POST("/users/:id/reset-password", handler.ResetUserPassword(cfg.Store))
		admin.POST("/users/:id/disable", handler.DisableUser(cfg.Store))
		admin.POST("/users/:id/enable", handler.EnableUser(cfg.Store))

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

		protected.GET("/tasks", handler.GetTasks(cfg.Store))
		protected.GET("/tasks/projects", handler.GetTaskProjects(cfg.Store))
		protected.POST("/tasks", handler.CreateTask(cfg.Store))
		protected.PATCH("/tasks/:id", handler.UpdateTask(cfg.Store))
		protected.DELETE("/tasks/:id", handler.DeleteTask(cfg.Store))
		protected.POST("/tasks/:id/occurrences/:date/complete", handler.CompleteOccurrence(cfg.Store))
		protected.POST("/tasks/:id/occurrences/:date/reopen", handler.ReopenOccurrence(cfg.Store))
		protected.POST("/tasks/:id/occurrences/:date/skip", handler.SkipOccurrence(cfg.Store))
		protected.GET("/task-occurrences", handler.GetTaskOccurrences(cfg.Store))
		protected.GET("/task-projects", handler.ListTaskProjects(cfg.Store))
		protected.POST("/task-projects", handler.CreateTaskProject(cfg.Store))
		protected.PATCH("/task-projects/:id", handler.UpdateTaskProject(cfg.Store))
		protected.DELETE("/task-projects/:id", handler.DeleteTaskProject(cfg.Store))
		protected.POST("/task-projects/:id/roadmap/generate", handler.GenerateLearningRoadmap(cfg.Store))
		protected.GET("/task-projects/:id/roadmap", handler.GetLearningRoadmap(cfg.Store))
		protected.POST("/roadmaps/:id/nodes", handler.CreateRoadmapNode(cfg.Store))
		protected.PATCH("/roadmap-nodes/:id", handler.UpdateRoadmapNode(cfg.Store))
		protected.DELETE("/roadmap-nodes/:id", handler.DeleteRoadmapNode(cfg.Store))
		protected.POST("/roadmap-nodes/:id/resources/search", handler.SearchRoadmapNodeResources(cfg.Store))
		protected.POST("/roadmap-nodes/:id/resources", handler.AddRoadmapNodeResource(cfg.Store))
		protected.PATCH("/roadmaps/:id/layout", handler.UpdateRoadmapLayout(cfg.Store))
		protected.POST("/roadmaps/:id/layout/optimize", handler.OptimizeRoadmapLayout(cfg.Store))
		protected.DELETE("/roadmap-resources/:id", handler.DeleteRoadmapResource(cfg.Store))

		protected.GET("/events", handler.GetEvents(cfg.Store))
		protected.POST("/events", handler.CreateEvent(cfg.Store))
		protected.PATCH("/events/:id", handler.UpdateEvent(cfg.Store))
		protected.DELETE("/events/:id", handler.DeleteEvent(cfg.Store))

		protected.GET("/calendar/project-sources", handler.GetCalendarProjectSources(cfg.Store))
		protected.PUT("/calendar/project-sources", handler.SaveCalendarProjectSources(cfg.Store))

		protected.GET("/inbox", handler.GetInbox(cfg.Store))
		protected.POST("/inbox", handler.CreateInboxItem(cfg.Store))
		protected.POST("/inbox/:id/convert", handler.ConvertInboxItem(cfg.Store))
		protected.POST("/inbox/batch", handler.BatchInbox(cfg.Store))
		protected.DELETE("/inbox/:id", handler.DeleteInboxItem(cfg.Store))

		protected.GET("/search", handler.Search(cfg.Store))
		protected.POST("/japanese/furigana", handler.JapaneseFurigana)
		protected.GET("/today", handler.GetToday(cfg.Store))
		protected.GET("/summary", handler.GetSummary(cfg.Store))
	}

	return r
}
