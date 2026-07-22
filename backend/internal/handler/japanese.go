package handler

import (
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/service"
)

const maxFuriganaTextRunes = 5000

type japaneseFuriganaRequest struct {
	Text string `json:"text"`
}

func JapaneseFurigana(c *gin.Context) {
	japaneseFuriganaWithChat(c, nil)
}

func JapaneseFuriganaWithAI(chat WorkspaceChatService) gin.HandlerFunc {
	return func(c *gin.Context) { japaneseFuriganaWithChat(c, chat) }
}

func japaneseFuriganaWithChat(c *gin.Context, chat WorkspaceChatService) {
	var req japaneseFuriganaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid request")
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		badRequest(c, "text is required")
		return
	}
	if utf8.RuneCountInString(req.Text) > maxFuriganaTextRunes {
		badRequest(c, "text is too long")
		return
	}

	var generator service.TextGenerator
	allowLocalFallback := true
	if identity, ok := auth.IdentityFromContext(c.Request.Context()); ok && chat != nil {
		if features, ok := chat.(WorkspaceAIFeatureService); ok {
			enabled, fallback, err := features.ResolveFeature(c.Request.Context(), identity.WorkspaceID, "japanese_furigana")
			if err != nil {
				internalError(c, "unable to resolve Japanese furigana settings")
				return
			}
			allowLocalFallback = fallback == "local"
			if enabled {
				generator = workspaceTextGenerator{service: chat, workspaceID: identity.WorkspaceID}
			}
		} else {
			generator = workspaceTextGenerator{service: chat, workspaceID: identity.WorkspaceID}
		}
	}
	segments, source, err := service.AnnotateJapaneseWithTextGeneratorPolicy(c.Request.Context(), req.Text, generator, allowLocalFallback)
	if err != nil {
		internalError(c, "failed to annotate Japanese text")
		return
	}
	success(c, model.FuriganaResponse{Segments: segments, Source: source})
}
