package router

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/handler"
	"github.com/hujinrun/flowspace/internal/middleware"
	"github.com/hujinrun/flowspace/internal/repository"
	"github.com/hujinrun/flowspace/internal/storage"
)

type Config struct {
	Store storage.Store
	Auth  config.AuthConfig
}

func Setup(cfg Config) *gin.Engine {
	repository.SetStore(cfg.Store)

	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery(), middleware.CORS())

	api := r.Group("/api")
	{
		api.GET("/health", handler.Health)

		authMiddleware := middleware.AuthMiddleware{Store: cfg.Store, SessionSecret: cfg.Auth.SessionSecret, Cookie: cfg.Auth.Cookie}
		authRoutes := api.Group("/auth")
		authRoutes.POST("/login", handler.Login(cfg.Store, cfg.Auth))
		authRoutes.POST("/logout", authMiddleware.Optional(), handler.Logout(cfg.Store, cfg.Auth.Cookie))
		authRoutes.GET("/me", authMiddleware.Required(), handler.Me(cfg.Store))
		authRoutes.POST("/change-password", authMiddleware.Required(), handler.ChangePassword(cfg.Store))

		protected := api.Group("")
		protected.Use(authMiddleware.Required(), authMiddleware.RequirePasswordSettled())

		protected.GET("/folders", handler.GetFolders)
		if cfg.Auth.EnableLocalDirectoryBrowser {
			systemAdmin := protected.Group("/system")
			systemAdmin.Use(authMiddleware.RequireAdmin())
			systemAdmin.GET("/directories", handler.ListLocalDirectories)
		}

		protected.GET("/notes", handler.GetNotes)
		protected.GET("/notes/:id", handler.GetNote)
		protected.POST("/notes", handler.CreateNote)
		protected.PATCH("/notes/:id", handler.UpdateNote)
		protected.DELETE("/notes/:id", handler.DeleteNote)
		protected.GET("/notes/:id/sync-binding", handler.GetNoteSyncBinding)
		protected.PUT("/notes/:id/sync-binding", handler.PutNoteSyncBinding)
		protected.DELETE("/notes/:id/sync-binding", handler.DeleteNoteSyncBinding)
		protected.GET("/notes/:id/sync-state", handler.GetNoteSyncState)

		protected.GET("/sync/targets", handler.ListSyncTargets)
		protected.POST("/sync/targets", handler.SaveSyncTarget)
		protected.PATCH("/sync/targets/:id", handler.UpdateSyncTarget)
		protected.DELETE("/sync/targets/:id", handler.DeleteSyncTarget)
		protected.POST("/sync/notes/:id", handler.SyncNote)
		protected.POST("/sync/targets/:target_id/push", handler.SyncTargetPush)
		protected.POST("/sync/targets/:target_id/pull", handler.SyncTargetPull)
		protected.POST("/sync/targets/:target_id/bidirectional", handler.SyncTargetBidirectional)
		protected.GET("/sync/targets/:target_id/deletions", handler.ListTargetDeletions)
		protected.POST("/sync/targets/:target_id/deletions/:note_id/confirm", handler.ConfirmTargetDeletion)
		protected.POST("/sync/targets/:target_id/deletions/:note_id/restore", handler.RestoreTargetDeletion)
		protected.POST("/sync/obsidian/test", handler.TestObsidianTarget)
		protected.POST("/sync/obsidian/notes/:id", handler.SyncObsidianNote)
		protected.POST("/sync/obsidian/folders/:folder_id", handler.SyncObsidianFolder)
		protected.POST("/sync/obsidian/all", handler.SyncObsidianAll)
		protected.POST("/sync/obsidian/pull", handler.SyncObsidianPull)
		protected.POST("/sync/obsidian/bidirectional", handler.SyncObsidianBidirectional)
		protected.GET("/sync/obsidian/deletions", handler.ListObsidianDeletions)
		protected.POST("/sync/obsidian/deletions/:note_id/confirm", handler.ConfirmObsidianDeletion)
		protected.POST("/sync/obsidian/deletions/:note_id/restore", handler.RestoreObsidianDeletion)
		protected.POST("/sync/notion/test", handler.TestNotionTarget)
		protected.POST("/sync/notion/all", handler.SyncNotionAll)
		protected.POST("/sync/notion/pull", handler.SyncNotionPull)
		protected.POST("/sync/notion/bidirectional", handler.SyncNotionBidirectional)
		protected.POST("/sync/notion/notes/:id", handler.SyncNotionNote)
		protected.GET("/sync/notion/deletions", handler.ListNotionDeletions)
		protected.POST("/sync/notion/deletions/:note_id/confirm", handler.ConfirmNotionDeletion)
		protected.POST("/sync/notion/deletions/:note_id/restore", handler.RestoreNotionDeletion)

		protected.GET("/tasks", handler.GetTasks)
		protected.GET("/tasks/projects", handler.GetTaskProjects)
		protected.POST("/tasks", handler.CreateTask)
		protected.PATCH("/tasks/:id", handler.UpdateTask)
		protected.DELETE("/tasks/:id", handler.DeleteTask)
		protected.POST("/tasks/:id/occurrences/:date/complete", handler.CompleteOccurrence)
		protected.POST("/tasks/:id/occurrences/:date/reopen", handler.ReopenOccurrence)
		protected.POST("/tasks/:id/occurrences/:date/skip", handler.SkipOccurrence)
		protected.GET("/task-occurrences", handler.GetTaskOccurrences)
		protected.GET("/task-projects", handler.ListTaskProjects)
		protected.POST("/task-projects", handler.CreateTaskProject)
		protected.PATCH("/task-projects/:id", handler.UpdateTaskProject)
		protected.DELETE("/task-projects/:id", handler.DeleteTaskProject)
		protected.POST("/task-projects/:id/roadmap/generate", handler.GenerateLearningRoadmap)
		protected.GET("/task-projects/:id/roadmap", handler.GetLearningRoadmap)
		protected.POST("/roadmaps/:id/nodes", handler.CreateRoadmapNode)
		protected.PATCH("/roadmap-nodes/:id", handler.UpdateRoadmapNode)
		protected.DELETE("/roadmap-nodes/:id", handler.DeleteRoadmapNode)
		protected.POST("/roadmap-nodes/:id/resources/search", handler.SearchRoadmapNodeResources)
		protected.POST("/roadmap-nodes/:id/resources", handler.AddRoadmapNodeResource)
		protected.PATCH("/roadmaps/:id/layout", handler.UpdateRoadmapLayout)
		protected.POST("/roadmaps/:id/layout/optimize", handler.OptimizeRoadmapLayout)
		protected.DELETE("/roadmap-resources/:id", handler.DeleteRoadmapResource)

		protected.GET("/events", handler.GetEvents)
		protected.POST("/events", handler.CreateEvent)
		protected.PATCH("/events/:id", handler.UpdateEvent)
		protected.DELETE("/events/:id", handler.DeleteEvent)

		protected.GET("/inbox", handler.GetInbox)
		protected.POST("/inbox", handler.CreateInboxItem)
		protected.POST("/inbox/:id/convert", handler.ConvertInboxItem)
		protected.POST("/inbox/batch", handler.BatchInbox)
		protected.DELETE("/inbox/:id", handler.DeleteInboxItem)

		protected.GET("/search", handler.Search)
		protected.GET("/today", handler.GetToday)
		protected.GET("/summary", handler.GetSummary)
	}

	return r
}
