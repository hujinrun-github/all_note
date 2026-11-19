package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/japaneseintegration"
	"github.com/hujinrun/flowspace/internal/storage"
)

// JapaneseIntegrationManager keeps cross-service credentials and task routing
// restricted to the owner/admin of the authenticated workspace.
func JapaneseIntegrationManager(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		scope, err := auth.ResolveWorkspaceContext(c.Request.Context())
		if err != nil {
			errorResponse(c, 401, "UNAUTHENTICATED", "请先登录当前工作区。")
			c.Abort()
			return
		}
		if store == nil {
			errorResponse(c, 503, "INTEGRATION_UNAVAILABLE", "日语联动暂不可用。")
			c.Abort()
			return
		}
		member, err := store.Auth().GetWorkspaceMembership(c.Request.Context(), scope.WorkspaceID, scope.ActorID)
		if err != nil || (member.Role != "owner" && member.Role != "admin") {
			errorResponse(c, 403, "FORBIDDEN", "需要工作区管理员权限。")
			c.Abort()
			return
		}
		c.Next()
	}
}

func RegisterJapaneseIntegrationRoutes(public, management *gin.RouterGroup, service *japaneseintegration.Service) {
	const base = "/integrations/japanese"
	scoped := func(action func(*gin.Context, auth.WorkspaceContext)) gin.HandlerFunc {
		return func(c *gin.Context) {
			scope, err := auth.ResolveWorkspaceContext(c.Request.Context())
			if err != nil {
				errorResponse(c, 401, "UNAUTHENTICATED", "请先登录当前工作区。")
				return
			}
			if service == nil {
				writeJapaneseError(c, japaneseintegration.ErrUnavailable)
				return
			}
			c.Header("Cache-Control", "no-store")
			action(c, scope)
		}
	}
	management.GET(base, scoped(func(c *gin.Context, scope auth.WorkspaceContext) {
		result, err := service.Status(c.Request.Context(), scope.WorkspaceID)
		if err != nil {
			writeJapaneseError(c, err)
			return
		}
		success(c, result)
	}))
	management.POST(base+"/pairing-codes", scoped(func(c *gin.Context, scope auth.WorkspaceContext) {
		result, err := service.CreatePairing(c.Request.Context(), scope.WorkspaceID, scope.ActorID)
		if err != nil {
			writeJapaneseError(c, err)
			return
		}
		created(c, result)
	}))
	management.POST(base+"/bindings", scoped(func(c *gin.Context, scope auth.WorkspaceContext) {
		var input japaneseintegration.BindingInput
		if !decodeJapaneseJSON(c, &input) {
			return
		}
		result, err := service.SaveBinding(c.Request.Context(), scope.WorkspaceID, scope.ActorID, input)
		if err != nil {
			writeJapaneseError(c, err)
			return
		}
		success(c, result)
	}))
	management.DELETE(base+"/connections/:connectionID", scoped(func(c *gin.Context, scope auth.WorkspaceContext) {
		if err := service.Revoke(c.Request.Context(), scope.WorkspaceID, scope.ActorID, c.Param("connectionID")); err != nil {
			writeJapaneseError(c, err)
			return
		}
		noContent(c)
	}))
	management.POST(base+"/events/:eventID/retry", scoped(func(c *gin.Context, scope auth.WorkspaceContext) {
		if err := service.Retry(c.Request.Context(), scope.WorkspaceID, c.Param("eventID")); err != nil {
			writeJapaneseError(c, err)
			return
		}
		success(c, gin.H{"queued": true})
	}))
	public.POST(base+"/pair", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		var input japaneseintegration.PairRequest
		if !decodeJapaneseJSON(c, &input) {
			return
		}
		if service == nil {
			writeJapaneseError(c, japaneseintegration.ErrUnavailable)
			return
		}
		result, err := service.Pair(c.Request.Context(), input)
		if err != nil {
			writeJapaneseError(c, err)
			return
		}
		success(c, result)
	})
	signed := func(action func(*gin.Context, japaneseintegration.Connection, []byte)) gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Header("Cache-Control", "no-store")
			if service == nil {
				writeJapaneseError(c, japaneseintegration.ErrUnavailable)
				return
			}
			body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10))
			if err != nil {
				writeJapaneseError(c, japaneseintegration.ErrInvalid)
				return
			}
			connection, err := service.Authenticate(c.Request.Context(), c.Request.Method, c.Request.URL.RequestURI(), c.Request.Header, body)
			if err != nil {
				writeJapaneseError(c, err)
				return
			}
			action(c, connection, body)
		}
	}
	public.PUT(base+"/catalog", signed(func(c *gin.Context, connection japaneseintegration.Connection, body []byte) {
		var input japaneseintegration.Catalog
		if !decodeJapaneseBytes(c, body, &input) {
			return
		}
		if err := service.PutCatalog(c.Request.Context(), connection, input); err != nil {
			writeJapaneseError(c, err)
			return
		}
		success(c, gin.H{"accepted": true})
	}))
	public.POST(base+"/events", signed(func(c *gin.Context, connection japaneseintegration.Connection, body []byte) {
		var input japaneseintegration.Event
		if !decodeJapaneseBytes(c, body, &input) {
			return
		}
		result, err := service.AcceptEvent(c.Request.Context(), connection, input)
		if err != nil {
			writeJapaneseError(c, err)
			return
		}
		status := http.StatusAccepted
		if result.Status == "applied" || result.Status == "already_done" || result.Status == "stale" {
			status = http.StatusOK
		}
		c.JSON(status, gin.H{"data": result})
	}))
	public.GET(base+"/events/:eventID", signed(func(c *gin.Context, connection japaneseintegration.Connection, _ []byte) {
		result, err := service.Event(c.Request.Context(), connection, c.Param("eventID"))
		if err != nil {
			writeJapaneseError(c, err)
			return
		}
		success(c, result)
	}))
}

func decodeJapaneseJSON(c *gin.Context, value any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 128<<10))
	if err != nil {
		writeJapaneseError(c, japaneseintegration.ErrInvalid)
		return false
	}
	return decodeJapaneseBytes(c, body, value)
}
func decodeJapaneseBytes(c *gin.Context, body []byte, value any) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeJapaneseError(c, japaneseintegration.ErrInvalid)
		return false
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		writeJapaneseError(c, japaneseintegration.ErrInvalid)
		return false
	}
	return true
}
func writeJapaneseError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, japaneseintegration.ErrInvalid):
		errorResponse(c, 400, "INTEGRATION_INVALID", "联动信息无效，请检查所选计划、任务和日期。")
	case errors.Is(err, japaneseintegration.ErrUnauthorized):
		errorResponse(c, 401, "INTEGRATION_UNAUTHORIZED", "连接凭据或配对码无效，请检查连接状态。")
	case errors.Is(err, japaneseintegration.ErrNotFound):
		errorResponse(c, 404, "INTEGRATION_NOT_FOUND", "没有找到对应的连接、关联或同步记录。")
	case errors.Is(err, japaneseintegration.ErrConflict):
		errorResponse(c, 409, "INTEGRATION_CONFLICT", "关联或计划已变化，请刷新后检查目标任务、时区及生效日期。")
	default:
		errorResponse(c, 503, "INTEGRATION_UNAVAILABLE", "日语联动暂不可用，请稍后重试。")
	}
}
