package router

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/handler"
	"github.com/hujinrun/flowspace/internal/middleware"
)

func Setup() *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery(), middleware.CORS())

	api := r.Group("/api")
	{
		api.GET("/folders", handler.GetFolders)
		api.GET("/system/directories", handler.ListLocalDirectories)

		api.GET("/notes", handler.GetNotes)
		api.GET("/notes/:id", handler.GetNote)
		api.POST("/notes", handler.CreateNote)
		api.PATCH("/notes/:id", handler.UpdateNote)
		api.DELETE("/notes/:id", handler.DeleteNote)
		api.GET("/notes/:id/sync-state", handler.GetNoteSyncState)

		api.GET("/sync/targets", handler.ListSyncTargets)
		api.POST("/sync/targets", handler.SaveSyncTarget)
		api.PATCH("/sync/targets/:id", handler.UpdateSyncTarget)
		api.POST("/sync/obsidian/test", handler.TestObsidianTarget)
		api.POST("/sync/obsidian/notes/:id", handler.SyncObsidianNote)
		api.POST("/sync/obsidian/folders/:folder_id", handler.SyncObsidianFolder)
		api.POST("/sync/obsidian/all", handler.SyncObsidianAll)
		api.POST("/sync/obsidian/pull", handler.SyncObsidianPull)
		api.POST("/sync/obsidian/bidirectional", handler.SyncObsidianBidirectional)
		api.GET("/sync/obsidian/deletions", handler.ListObsidianDeletions)
		api.POST("/sync/obsidian/deletions/:note_id/confirm", handler.ConfirmObsidianDeletion)
		api.POST("/sync/obsidian/deletions/:note_id/restore", handler.RestoreObsidianDeletion)
		api.POST("/sync/notion/test", handler.TestNotionTarget)
		api.POST("/sync/notion/all", handler.SyncNotionAll)
		api.POST("/sync/notion/pull", handler.SyncNotionPull)
		api.POST("/sync/notion/bidirectional", handler.SyncNotionBidirectional)
		api.POST("/sync/notion/notes/:id", handler.SyncNotionNote)
		api.GET("/sync/notion/deletions", handler.ListNotionDeletions)
		api.POST("/sync/notion/deletions/:note_id/confirm", handler.ConfirmNotionDeletion)
		api.POST("/sync/notion/deletions/:note_id/restore", handler.RestoreNotionDeletion)

		api.GET("/tasks", handler.GetTasks)
		api.GET("/tasks/projects", handler.GetTaskProjects)
		api.POST("/tasks", handler.CreateTask)
		api.PATCH("/tasks/:id", handler.UpdateTask)
		api.DELETE("/tasks/:id", handler.DeleteTask)
		api.GET("/task-projects", handler.ListTaskProjects)
		api.POST("/task-projects", handler.CreateTaskProject)
		api.PATCH("/task-projects/:id", handler.UpdateTaskProject)
		api.DELETE("/task-projects/:id", handler.DeleteTaskProject)
		api.POST("/task-projects/:id/roadmap/generate", handler.GenerateLearningRoadmap)
		api.GET("/task-projects/:id/roadmap", handler.GetLearningRoadmap)
		api.POST("/roadmaps/:id/nodes", handler.CreateRoadmapNode)
		api.PATCH("/roadmap-nodes/:id", handler.UpdateRoadmapNode)
		api.DELETE("/roadmap-nodes/:id", handler.DeleteRoadmapNode)
		api.POST("/roadmap-nodes/:id/resources/search", handler.SearchRoadmapNodeResources)
		api.POST("/roadmap-nodes/:id/resources", handler.AddRoadmapNodeResource)
		api.PATCH("/roadmaps/:id/layout", handler.UpdateRoadmapLayout)
		api.POST("/roadmaps/:id/layout/optimize", handler.OptimizeRoadmapLayout)
		api.DELETE("/roadmap-resources/:id", handler.DeleteRoadmapResource)

		api.GET("/events", handler.GetEvents)
		api.POST("/events", handler.CreateEvent)
		api.PATCH("/events/:id", handler.UpdateEvent)
		api.DELETE("/events/:id", handler.DeleteEvent)

		api.GET("/inbox", handler.GetInbox)
		api.POST("/inbox", handler.CreateInboxItem)
		api.POST("/inbox/:id/convert", handler.ConvertInboxItem)
		api.POST("/inbox/batch", handler.BatchInbox)
		api.DELETE("/inbox/:id", handler.DeleteInboxItem)

		api.GET("/search", handler.Search)
		api.GET("/today", handler.GetToday)
		api.GET("/summary", handler.GetSummary)
	}

	return r
}
