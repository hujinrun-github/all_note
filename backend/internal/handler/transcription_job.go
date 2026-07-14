package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/transcriptionjob"
)

type createTranscriptionJobRequest struct {
	Language string `json:"language" binding:"required"`
}

func CreateMobileTranscriptionJob(store storage.Store) gin.HandlerFunc {
	service := transcriptionjob.NewService()
	return func(c *gin.Context) {
		var request createTranscriptionJobRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			mobileError(c, http.StatusBadRequest, "invalid_request", "language is required", false)
			return
		}
		job, err := service.Create(
			c.Request.Context(), store, c.GetHeader("Idempotency-Key"), c.Param("clientID"), request.Language,
		)
		if err != nil {
			handleMobileTranscriptionJobError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, job)
	}
}

func GetMobileTranscriptionJob(store storage.Store) gin.HandlerFunc {
	service := transcriptionjob.NewService()
	return func(c *gin.Context) {
		job, err := service.Get(c.Request.Context(), store, c.Param("jobID"))
		if err != nil {
			handleMobileTranscriptionJobError(c, err)
			return
		}
		c.JSON(http.StatusOK, job)
	}
}

func RetryMobileTranscriptionJob(store storage.Store) gin.HandlerFunc {
	service := transcriptionjob.NewService()
	return func(c *gin.Context) {
		job, err := service.Retry(c.Request.Context(), store, c.GetHeader("Idempotency-Key"), c.Param("jobID"))
		if err != nil {
			handleMobileTranscriptionJobError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, job)
	}
}

func handleMobileTranscriptionJobError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, transcriptionjob.ErrInvalidRequest):
		mobileError(c, http.StatusBadRequest, "invalid_request", "invalid transcription job request", false)
	case errors.Is(err, storage.ErrMutationIDReused):
		mobileError(c, http.StatusConflict, "mutation_id_reused", "idempotency key was reused with a different request", false)
	case errors.Is(err, storage.ErrTranscriptionJobNotRetryable):
		mobileError(c, http.StatusConflict, "job_not_retryable", "transcription job is not in a retryable terminal state", false)
	case errors.Is(err, storage.ErrStaleTranscriptionJob):
		mobileError(c, http.StatusConflict, "stale_job", "only the latest failed transcription job can be retried", false)
	case errors.Is(err, storage.ErrMobileEntityNotFound):
		mobileError(c, http.StatusNotFound, "not_found", "voice note or transcription job was not found", false)
	default:
		mobileError(c, http.StatusInternalServerError, "transcription_job_unavailable", "transcription job service is unavailable", true)
	}
}
