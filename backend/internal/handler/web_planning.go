package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/webplanning"
)

// RegisterWebPlanningRoutes uses the ordinary session-authenticated Web group.
// The application repeats owner/workspace checks; mobile-v1 is never involved.
func RegisterWebPlanningRoutes(routes *gin.RouterGroup, service *webplanning.Service) {
	handle := func(action func(*gin.Context, webplanning.Scope)) gin.HandlerFunc {
		return func(c *gin.Context) {
			identity, err := auth.ResolveWorkspaceContext(c.Request.Context())
			if err != nil {
				errorResponse(c, http.StatusUnauthorized, "UNAUTHORIZED", "请先登录当前工作区。")
				return
			}
			if service == nil {
				writePlanningError(c, webplanning.ErrUnavailable)
				return
			}
			action(c, webplanning.Scope{WorkspaceID: identity.WorkspaceID, ActorID: identity.ActorID})
		}
	}
	routes.GET("/planning/capabilities", handle(func(c *gin.Context, scope webplanning.Scope) {
		result, err := service.Capabilities(c.Request.Context(), scope)
		if err != nil {
			writePlanningError(c, err)
			return
		}
		success(c, result)
	}))
	routes.POST("/plan-drafts", handle(func(c *gin.Context, scope webplanning.Scope) {
		var body webplanning.CreateRequest
		if !decodePlanning(c, &body) {
			return
		}
		draft, err := service.Create(c.Request.Context(), scope, body)
		if err != nil {
			writePlanningError(c, err)
			return
		}
		created(c, gin.H{"draft": draft})
	}))
	routes.GET("/plan-drafts", handle(func(c *gin.Context, scope webplanning.Scope) {
		drafts, err := service.List(c.Request.Context(), scope)
		if err != nil {
			writePlanningError(c, err)
			return
		}
		success(c, gin.H{"drafts": drafts})
	}))
	routes.GET("/plan-drafts/:id", handle(func(c *gin.Context, scope webplanning.Scope) {
		draft, err := service.Get(c.Request.Context(), scope, c.Param("id"))
		if err != nil {
			writePlanningError(c, err)
			return
		}
		success(c, gin.H{"draft": draft})
	}))
	routes.PATCH("/plan-drafts/:id", handle(func(c *gin.Context, scope webplanning.Scope) {
		var body webplanning.PatchRequest
		if !decodePlanning(c, &body) {
			return
		}
		draft, err := service.Patch(c.Request.Context(), scope, c.Param("id"), body)
		if err != nil {
			writePlanningError(c, err)
			return
		}
		success(c, gin.H{"draft": draft})
	}))
	routes.DELETE("/plan-drafts/:id", handle(func(c *gin.Context, scope webplanning.Scope) {
		if err := service.Discard(c.Request.Context(), scope, c.Param("id")); err != nil {
			writePlanningError(c, err)
			return
		}
		noContent(c)
	}))
	routes.PUT("/plan-drafts/:id/audio", handle(func(c *gin.Context, scope webplanning.Scope) {
		expected, err := strconv.ParseInt(c.Query("expected_revision"), 10, 64)
		if err != nil || expected < 1 {
			writePlanningError(c, webplanning.ErrInvalid)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, webplanning.MaxAudioBytes)
		draft, err := service.Upload(c.Request.Context(), scope, c.Param("id"), expected, c.ContentType(), c.Request.Body)
		if err != nil {
			writePlanningError(c, err)
			return
		}
		success(c, gin.H{"draft": draft})
	}))
	routes.GET("/plan-drafts/:id/audio", handle(func(c *gin.Context, scope webplanning.Scope) {
		object, err := service.Audio(c.Request.Context(), scope, c.Param("id"))
		if err != nil {
			writePlanningError(c, err)
			return
		}
		defer object.Body.Close()
		audio, err := io.ReadAll(io.LimitReader(object.Body, webplanning.MaxAudioBytes+1))
		if err != nil || int64(len(audio)) > webplanning.MaxAudioBytes {
			writePlanningError(c, webplanning.ErrUnavailable)
			return
		}
		c.Header("Content-Type", object.ContentType)
		c.Header("Cache-Control", "private, no-store")
		c.Header("X-Content-Type-Options", "nosniff")
		http.ServeContent(c.Writer, c.Request, "recording", time.Time{}, bytes.NewReader(audio))
	}))
	for _, stage := range []string{"transcribe", "generate"} {
		stage := stage
		routes.POST("/plan-drafts/:id/"+stage, handle(func(c *gin.Context, scope webplanning.Scope) {
			var body webplanning.StartRequest
			if !decodePlanning(c, &body) {
				return
			}
			result, err := service.Start(c.Request.Context(), scope, c.Param("id"), stage, body)
			if err != nil {
				writePlanningError(c, err)
				return
			}
			c.JSON(http.StatusAccepted, model.APIResponse{Data: result})
		}))
	}
	routes.POST("/plan-drafts/:id/validate", handle(func(c *gin.Context, scope webplanning.Scope) {
		var body struct {
			ExpectedRevision int64 `json:"expected_revision"`
		}
		if !decodePlanning(c, &body) {
			return
		}
		result, err := service.Validate(c.Request.Context(), scope, c.Param("id"), body.ExpectedRevision)
		if err != nil {
			writePlanningError(c, err)
			return
		}
		success(c, result)
	}))
	routes.POST("/plan-drafts/:id/commit", handle(func(c *gin.Context, scope webplanning.Scope) {
		var body webplanning.CommitRequest
		if !decodePlanning(c, &body) {
			return
		}
		receipt, err := service.Commit(c.Request.Context(), scope, c.Param("id"), body)
		if err != nil {
			writePlanningError(c, err)
			return
		}
		success(c, gin.H{"receipt": receipt})
	}))
	routes.GET("/plan-jobs/:id", handle(func(c *gin.Context, scope webplanning.Scope) {
		job, err := service.Job(c.Request.Context(), scope, c.Param("id"))
		if err != nil {
			writePlanningError(c, err)
			return
		}
		success(c, gin.H{"job": job})
	}))
	routes.POST("/plan-jobs/:id/retry", handle(func(c *gin.Context, scope webplanning.Scope) {
		var body struct {
			RequestID string `json:"request_id"`
		}
		if !decodePlanning(c, &body) {
			return
		}
		result, err := service.Retry(c.Request.Context(), scope, c.Param("id"), body.RequestID)
		if err != nil {
			writePlanningError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, model.APIResponse{Data: result})
	}))
	routes.POST("/plan-jobs/:id/cancel", handle(func(c *gin.Context, scope webplanning.Scope) {
		var body struct {
			RequestID string `json:"request_id"`
		}
		if !decodePlanning(c, &body) {
			return
		}
		result, err := service.Cancel(c.Request.Context(), scope, c.Param("id"))
		if err != nil {
			writePlanningError(c, err)
			return
		}
		success(c, result)
	}))
}
func decodePlanning(c *gin.Context, target any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024*1024)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		errorResponse(c, http.StatusBadRequest, "PLANNING_INVALID_INPUT", "请求格式不正确，请检查输入。")
		return false
	}
	return true
}
func writePlanningError(c *gin.Context, err error) {
	var maxErr *http.MaxBytesError
	switch {
	case errors.As(err, &maxErr):
		errorResponse(c, http.StatusRequestEntityTooLarge, "PLANNING_AUDIO_TOO_LARGE", "录音文件过大，请缩短后重试。")
	case errors.Is(err, webplanning.ErrNotFound), errors.Is(err, objectstore.ErrNotFound):
		errorResponse(c, http.StatusNotFound, "PLANNING_NOT_FOUND", "没有找到这份草稿或录音，可能已被删除。")
	case errors.Is(err, webplanning.ErrConflict):
		errorResponse(c, http.StatusConflict, "PLANNING_CONFLICT", "草稿或项目已更新，请重新载入后核对。你的输入仍保留。")
	case errors.Is(err, webplanning.ErrInvalid):
		errorResponse(c, http.StatusBadRequest, "PLANNING_INVALID_INPUT", "输入或安排不完整，请检查日期、重复规则和任务内容。")
	case errors.Is(err, webplanning.ErrUnavailable), errors.Is(err, objectstore.ErrUnavailable):
		errorResponse(c, http.StatusServiceUnavailable, "PLANNING_UNAVAILABLE", "计划服务暂不可用，请检查文本 AI、语音转写或对象存储配置。")
	default:
		errorResponse(c, http.StatusServiceUnavailable, "PLANNING_UNAVAILABLE", "暂时无法处理，请稍后重试。")
	}
}
