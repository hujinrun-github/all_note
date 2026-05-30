package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/service"
)

func GetNotes(c *gin.Context) {
	page, pageSize := getPagination(c)
	folderID := c.Query("folder_id")
	sort := c.DefaultQuery("sort", "recent")

	notes, total, err := service.GetNotes(folderID, sort, page, pageSize)
	if err != nil {
		internalError(c, "failed to get notes")
		return
	}
	successWithPagination(c, gin.H{"notes": notes}, page, pageSize, total)
}

func GetNote(c *gin.Context) {
	note, err := service.GetNote(c.Param("id"))
	if err != nil {
		notFound(c, "note not found")
		return
	}
	success(c, gin.H{"note": note})
}

func CreateNote(c *gin.Context) {
	var req model.CreateNoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "title is required")
		return
	}
	note, err := service.CreateNote(&req)
	if err != nil {
		internalError(c, "failed to create note")
		return
	}
	created(c, gin.H{"note": note})
}

func UpdateNote(c *gin.Context) {
	var req model.UpdateNoteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid request body")
		return
	}
	note, err := service.UpdateNote(c.Param("id"), &req)
	if err != nil {
		notFound(c, "note not found")
		return
	}
	success(c, gin.H{"note": note})
}

func DeleteNote(c *gin.Context) {
	if err := service.DeleteNote(c.Param("id")); err != nil {
		internalError(c, "failed to delete note")
		return
	}
	noContent(c)
}
