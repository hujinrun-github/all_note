package handler

import (
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/service"
)

const maxFuriganaTextRunes = 5000

type japaneseFuriganaRequest struct {
	Text string `json:"text"`
}

func JapaneseFurigana(c *gin.Context) {
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

	segments, source, err := service.AnnotateJapaneseWithAI(c.Request.Context(), req.Text)
	if err != nil {
		internalError(c, "failed to annotate Japanese text")
		return
	}
	success(c, model.FuriganaResponse{Segments: segments, Source: source})
}
