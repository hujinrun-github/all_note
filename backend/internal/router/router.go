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

		api.GET("/tasks", handler.GetTasks)
		api.POST("/tasks", handler.CreateTask)
		api.PATCH("/tasks/:id", handler.UpdateTask)
		api.DELETE("/tasks/:id", handler.DeleteTask)

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
	}

	return r
}
