package handler

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/codexsubscription"
)

type CodexSubscriptionService interface {
	Start(context.Context, string, string) (codexsubscription.StartResult, error)
	Poll(context.Context, string, string, string, int64, int64) (codexsubscription.PollResult, error)
}

func StartCodexSubscription(service CodexSubscriptionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := codexIdentity(c, service)
		if !ok {
			return
		}
		result, err := service.Start(c.Request.Context(), identity.UserID, identity.WorkspaceID)
		if err != nil {
			errorResponse(c, http.StatusBadGateway, "CODEX_AUTH_START_FAILED", "unable to start Codex authorization")
			return
		}
		created(c, result)
	}
}

func PollCodexSubscription(service CodexSubscriptionService) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := codexIdentity(c, service)
		if !ok {
			return
		}
		flowID := strings.TrimSpace(c.Param("flowID"))
		var request struct {
			ExpectedRevision        int64 `json:"expected_revision"`
			ExpectedRuntimeRevision int64 `json:"expected_runtime_revision"`
		}
		if flowID == "" || c.ShouldBindJSON(&request) != nil {
			badRequest(c, "invalid Codex authorization poll request")
			return
		}
		result, err := service.Poll(c.Request.Context(), identity.UserID, identity.WorkspaceID, flowID, request.ExpectedRevision, request.ExpectedRuntimeRevision)
		if err != nil {
			errorResponse(c, http.StatusBadGateway, "CODEX_AUTH_POLL_FAILED", "unable to complete Codex authorization")
			return
		}
		success(c, result)
	}
}

func codexIdentity(c *gin.Context, service CodexSubscriptionService) (auth.RequestIdentity, bool) {
	identity, ok := auth.IdentityFromContext(c.Request.Context())
	if !ok {
		errorResponse(c, http.StatusUnauthorized, "UNAUTHENTICATED", "authentication required")
		return auth.RequestIdentity{}, false
	}
	if service == nil {
		errorResponse(c, http.StatusServiceUnavailable, "CODEX_SUBSCRIPTION_UNAVAILABLE", "Codex subscription is unavailable")
		return auth.RequestIdentity{}, false
	}
	return identity, true
}
