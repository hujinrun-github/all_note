package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/service"
	"github.com/hujinrun/flowspace/internal/storage"
)

func GetNotes(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, pageSize := getPagination(c)
		folderID := c.Query("folder_id")
		projectID := c.Query("project_id")
		unassigned := c.Query("unassigned") == "true"
		sort := c.DefaultQuery("sort", "recent")

		if projectID != "" && unassigned {
			badRequest(c, "project_id and unassigned cannot be used together")
			return
		}

		notes, total, err := service.GetNotes(c.Request.Context(), store, folderID, projectID, sort, unassigned, page, pageSize)
		if err != nil {
			internalError(c, "failed to get notes")
			return
		}
		successWithPagination(c, gin.H{"notes": notes}, page, pageSize, total)
	}
}

func GetNote(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		note, err := service.GetNote(c.Request.Context(), store, c.Param("id"))
		if err != nil {
			notFound(c, "note not found")
			return
		}
		success(c, gin.H{"note": note})
	}
}

func CreateNote(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.CreateNoteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "title is required")
			return
		}
		note, err := service.CreateNote(c.Request.Context(), store, &req)
		if err != nil {
			internalError(c, "failed to create note")
			return
		}
		created(c, gin.H{"note": note})
	}
}

func UpdateNote(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.UpdateNoteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "invalid request body")
			return
		}
		note, err := service.UpdateNote(c.Request.Context(), store, c.Param("id"), &req)
		if err != nil {
			if err.Error() == "note not found" {
				notFound(c, "note not found")
			} else {
				internalError(c, "failed to update note")
			}
			return
		}
		success(c, gin.H{"note": note})
	}
}

func DeleteNote(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := service.DeleteNote(c.Request.Context(), store, c.Param("id")); err != nil {
			internalError(c, "failed to delete note")
			return
		}
		noContent(c)
	}
}
