package handler

import (
	"database/sql"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/service"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/transcription"
)

func AuthorizeWatchDevice(store storage.Store, sessionSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.AuthorizeWatchDeviceRequest
		if c.Request.ContentLength != 0 {
			if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
				badRequest(c, "invalid request body")
				return
			}
		}
		response, err := service.AuthorizeWatchDevice(c.Request.Context(), store, sessionSecret, req)
		if err != nil {
			handleNativeError(c, err, http.StatusInternalServerError)
			return
		}
		created(c, response)
	}
}

func RevokeWatchDevice(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.RevokeWatchDeviceRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "device_id is required")
			return
		}
		if err := service.RevokeWatchDevice(c.Request.Context(), store, req.DeviceID); err != nil {
			handleNativeError(c, err, http.StatusInternalServerError)
			return
		}
		noContent(c)
	}
}

func CreateVoiceNote(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.CreateVoiceNoteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "client_id is required")
			return
		}
		voice, wasCreated, err := service.CreateVoiceNote(c.Request.Context(), store, req)
		if err != nil {
			handleNativeError(c, err, http.StatusInternalServerError)
			return
		}
		payload := gin.H{"voice_note": voice}
		if wasCreated {
			created(c, payload)
			return
		}
		success(c, payload)
	}
}

func UploadVoiceAudio(store storage.Store, objects objectstore.Store, maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		voice, err := service.UploadVoiceAudio(
			c.Request.Context(),
			store,
			objects,
			c.Param("clientID"),
			c.GetHeader("Content-Type"),
			c.GetHeader("X-Audio-SHA256"),
			c.Request.Body,
			c.Request.ContentLength,
			maxBytes,
		)
		if err != nil {
			handleNativeError(c, err, http.StatusInternalServerError)
			return
		}
		success(c, gin.H{"voice_note": voice})
	}
}

func GetVoiceAudio(store storage.Store, objects objectstore.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		voice, object, err := service.GetVoiceAudio(c.Request.Context(), store, objects, c.Param("clientID"))
		if err != nil {
			handleNativeError(c, err, http.StatusInternalServerError)
			return
		}
		defer object.Body.Close()
		contentType := object.ContentType
		if strings.TrimSpace(contentType) == "" {
			contentType = voice.MimeType
		}
		headers := map[string]string{
			"Cache-Control": "private, max-age=3600",
			"ETag":          object.ETag,
		}
		c.DataFromReader(http.StatusOK, object.Size, contentType, object.Body, headers)
	}
}

func GetVoiceNoteStatus(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		voice, err := service.GetVoiceNote(c.Request.Context(), store, c.Param("clientID"))
		if err != nil {
			handleNativeError(c, err, http.StatusInternalServerError)
			return
		}
		success(c, model.VoiceNoteStatusResponse{VoiceNote: *voice})
	}
}

func TranscribeVoiceNote(store storage.Store, objects objectstore.Store, transcriber transcription.Transcriber) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req model.VoiceTranscriptionRequest
		if c.Request.ContentLength != 0 {
			if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
				badRequest(c, "invalid request body")
				return
			}
		}
		voice, err := service.TranscribeVoiceNote(c.Request.Context(), store, objects, transcriber, c.Param("clientID"), req.Language)
		if err != nil {
			handleNativeError(c, err, http.StatusBadGateway)
			return
		}
		success(c, gin.H{"voice_note": voice})
	}
}

func GetWatchSnapshot(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		snapshot, err := service.GetWatchSnapshot(c.Request.Context(), store)
		if err != nil {
			handleNativeError(c, err, http.StatusInternalServerError)
			return
		}
		success(c, snapshot)
	}
}

func handleNativeError(c *gin.Context, err error, fallbackStatus int) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		notFound(c, "resource not found")
	case errors.Is(err, service.ErrInvalidVoiceClientID), errors.Is(err, service.ErrInvalidVoiceMetadata):
		badRequest(c, err.Error())
	case errors.Is(err, service.ErrVoiceAudioTooLarge):
		errorResponse(c, http.StatusRequestEntityTooLarge, "AUDIO_TOO_LARGE", err.Error())
	case errors.Is(err, service.ErrVoiceAudioChecksum):
		errorResponse(c, http.StatusUnprocessableEntity, "CHECKSUM_MISMATCH", err.Error())
	case errors.Is(err, service.ErrVoiceAudioType):
		errorResponse(c, http.StatusUnsupportedMediaType, "UNSUPPORTED_AUDIO_TYPE", err.Error())
	case errors.Is(err, storage.ErrUploadConflict):
		conflict(c, "AUDIO_CONFLICT", err.Error())
	case errors.Is(err, service.ErrVoiceAudioNotUploaded):
		conflict(c, "AUDIO_NOT_UPLOADED", err.Error())
	case errors.Is(err, service.ErrVoiceStorageUnavailable), errors.Is(err, objectstore.ErrUnavailable):
		errorResponse(c, http.StatusServiceUnavailable, "VOICE_STORAGE_UNAVAILABLE", "voice storage is unavailable")
	case errors.Is(err, service.ErrTranscriptionUnavailable), errors.Is(err, transcription.ErrUnavailable):
		errorResponse(c, http.StatusServiceUnavailable, "TRANSCRIPTION_UNAVAILABLE", "transcription service is unavailable")
	case errors.Is(err, objectstore.ErrNotFound):
		notFound(c, "audio object not found")
	default:
		errorResponse(c, fallbackStatus, "NATIVE_SERVICE_ERROR", "native app request failed")
	}
}
