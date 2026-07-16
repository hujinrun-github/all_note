package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

type mobileConflictResolutionRequest struct {
	MutationID       string          `json:"mutation_id"`
	ConflictRevision int64           `json:"conflict_revision"`
	TargetRevision   int64           `json:"target_revision"`
	Resolution       string          `json:"resolution"`
	MergedPayload    json.RawMessage `json:"merged_payload,omitempty"`
}

func ListMobileConflicts(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		syncRepo, err := storage.MobileSyncRepositoryFrom(store)
		if err != nil {
			mobileError(c, http.StatusInternalServerError, "mobile_sync_unavailable", "mobile sync is unavailable", true)
			return
		}
		conflicts, err := syncRepo.ListUnresolvedConflicts(c.Request.Context())
		if err != nil {
			mobileError(c, http.StatusInternalServerError, "mobile_sync_unavailable", "mobile sync is unavailable", true)
			return
		}
		c.JSON(http.StatusOK, conflicts)
	}
}

func ResolveMobileConflict(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		conflictID := strings.TrimSpace(c.Param("conflictID"))
		if _, err := uuid.Parse(conflictID); err != nil {
			mobileError(c, http.StatusBadRequest, "invalid_conflict_id", "conflictID must be a UUID", false)
			return
		}
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		var request mobileConflictResolutionRequest
		if err := decoder.Decode(&request); err != nil || ensureRequestEOF(decoder) != nil {
			mobileError(c, http.StatusBadRequest, "invalid_request", "invalid conflict resolution", false)
			return
		}
		if _, err := uuid.Parse(request.MutationID); err != nil {
			mobileError(c, http.StatusBadRequest, "invalid_mutation_id", "mutation_id must be a UUID", false)
			return
		}
		encoded, err := json.Marshal(request)
		if err != nil {
			mobileError(c, http.StatusBadRequest, "invalid_request", "invalid conflict resolution", false)
			return
		}
		digest := sha256.Sum256(encoded)
		syncRepo, err := storage.MobileSyncRepositoryFrom(store)
		if err != nil {
			mobileError(c, http.StatusInternalServerError, "mobile_sync_unavailable", "mobile sync is unavailable", true)
			return
		}
		entity, err := syncRepo.ResolveConflict(c.Request.Context(), model.ResolveMobileSyncConflict{
			ConflictID: conflictID, MutationID: request.MutationID, RequestSHA256: hex.EncodeToString(digest[:]),
			ConflictRevision: request.ConflictRevision, TargetRevision: request.TargetRevision,
			Resolution: request.Resolution, MergedPayload: request.MergedPayload,
		})
		if err != nil {
			switch {
			case errors.Is(err, storage.ErrMobileConflictNotFound):
				mobileError(c, http.StatusNotFound, "conflict_not_found", "the conflict was not found", false)
			case errors.Is(err, storage.ErrMobileConflictAdvanced):
				mobileError(c, http.StatusConflict, "conflict_advanced", "the conflict was already changed", false)
			case errors.Is(err, storage.ErrMobileTargetAdvanced):
				mobileError(c, http.StatusConflict, "target_advanced", "the target entity changed after the conflict was created", false)
			case errors.Is(err, storage.ErrMutationIDReused):
				mobileError(c, http.StatusConflict, "mutation_id_reused", "mutation_id was reused with a different request", false)
			default:
				mobileError(c, http.StatusBadRequest, "invalid_resolution", "the conflict resolution could not be applied", false)
			}
			return
		}
		c.JSON(http.StatusOK, mobileEntityToWire(*entity))
	}
}
