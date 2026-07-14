package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/mobilesync"
	"github.com/hujinrun/flowspace/internal/storage"
)

func ApplyMobileMutations(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > mobilesync.MaxBatchBytes {
			mobileError(c, http.StatusRequestEntityTooLarge, "batch_too_large", "mutation batch exceeds 1 MiB", false)
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, mobilesync.MaxBatchBytes)
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		var batch mobilesync.MutationBatch
		if err := decoder.Decode(&batch); err != nil {
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				mobileError(c, http.StatusRequestEntityTooLarge, "batch_too_large", "mutation batch exceeds 1 MiB", false)
				return
			}
			mobileError(c, http.StatusBadRequest, "invalid_request", "invalid mutation batch", false)
			return
		}
		if err := ensureRequestEOF(decoder); err != nil {
			mobileError(c, http.StatusBadRequest, "invalid_request", "request body must contain one JSON object", false)
			return
		}
		result, err := mobilesync.ApplyBatch(c.Request.Context(), store, batch)
		if err != nil {
			switch {
			case errors.Is(err, mobilesync.ErrBatchTooLarge):
				mobileError(c, http.StatusRequestEntityTooLarge, "batch_too_large", err.Error(), false)
			case errors.Is(err, mobilesync.ErrInvalidBatch):
				mobileError(c, http.StatusBadRequest, "invalid_request", err.Error(), false)
			default:
				mobileError(c, http.StatusInternalServerError, "mobile_sync_unavailable", "mobile sync is unavailable", true)
			}
			return
		}
		c.JSON(http.StatusOK, result)
	}
}

func ensureRequestEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("multiple JSON values")
	}
	return err
}

func mobileError(c *gin.Context, status int, code, message string, retryable bool) {
	c.JSON(status, gin.H{
		"schema_version": "mobile-v1",
		"type":           "error",
		"code":           code,
		"message":        message,
		"retryable":      retryable,
	})
}
