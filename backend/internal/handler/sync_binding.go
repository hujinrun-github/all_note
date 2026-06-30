package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

var (
	errSyncBindingConflict                = errors.New("sync binding conflict")
	errSyncBindingTargetChangeUnconfirmed = errors.New("sync binding target change requires confirmation")
	errSyncBindingTargetDisabled          = errors.New("sync binding target disabled")
)

func GetNoteSyncBinding(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		noteID := strings.TrimSpace(c.Param("id"))
		if noteID == "" {
			badRequest(c, "note id is required")
			return
		}

		response, err := buildNoteSyncBindingResponse(c.Request.Context(), store.Sync(), noteID)
		if err != nil {
			internalError(c, "failed to get note sync binding")
			return
		}
		success(c, response)
	}
}

func PutNoteSyncBinding(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		noteID := strings.TrimSpace(c.Param("id"))
		if noteID == "" {
			badRequest(c, "note id is required")
			return
		}

		var req model.SaveNoteSyncBindingRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "invalid sync binding")
			return
		}
		req.TargetID = strings.TrimSpace(req.TargetID)
		if req.TargetID == "" {
			badRequest(c, "target_id is required")
			return
		}

		var savedBinding model.NoteSyncBinding
		var savedTarget model.SyncTarget
		changedTarget := false
		err := store.Transact(c.Request.Context(), func(txStore storage.Store) error {
			syncRepo := txStore.Sync()
			if err := syncRepo.LockBindingSlot(c.Request.Context(), noteID); err != nil {
				return err
			}
			target, err := syncRepo.GetTarget(c.Request.Context(), req.TargetID)
			if err != nil {
				return err
			}
			if !target.Enabled {
				return errSyncBindingTargetDisabled
			}

			existing, err := syncRepo.GetBinding(c.Request.Context(), noteID)
			switch {
			case err == nil:
				if strings.TrimSpace(req.ExpectedTargetID) != "" && req.ExpectedTargetID != existing.TargetID {
					return errSyncBindingConflict
				}
				changedTarget = existing.TargetID != req.TargetID
				if changedTarget && !req.ConfirmChangedTarget {
					return errSyncBindingTargetChangeUnconfirmed
				}
				if changedTarget {
					if err := tombstoneAndReleaseClaim(c.Request.Context(), syncRepo, noteID, "target_changed"); err != nil {
						return err
					}
				}
			case errors.Is(err, sql.ErrNoRows):
				if strings.TrimSpace(req.ExpectedTargetID) != "" {
					return errSyncBindingConflict
				}
			default:
				return err
			}

			if err := syncRepo.DeleteSuppression(c.Request.Context(), noteID, req.TargetID); err != nil {
				return err
			}
			if err := syncRepo.DeleteImportTombstonesForNoteTarget(c.Request.Context(), noteID, req.TargetID); err != nil {
				return err
			}
			if err := syncRepo.PutBinding(c.Request.Context(), model.NoteSyncBinding{NoteID: noteID, TargetID: req.TargetID}); err != nil {
				return err
			}
			binding, err := syncRepo.GetBinding(c.Request.Context(), noteID)
			if err != nil {
				return err
			}
			savedBinding = *binding
			savedTarget = *target
			return nil
		})
		if errors.Is(err, sql.ErrNoRows) {
			notFound(c, "sync target not found")
			return
		}
		if errors.Is(err, errSyncBindingTargetDisabled) {
			badRequest(c, "sync target is disabled")
			return
		}
		if errors.Is(err, errSyncBindingTargetChangeUnconfirmed) {
			errorResponse(c, http.StatusConflict, "target_change_requires_confirm", "changing sync target requires confirmation")
			return
		}
		if errors.Is(err, errSyncBindingConflict) {
			errorResponse(c, http.StatusConflict, "sync_binding_conflict", "sync binding was changed by another request")
			return
		}
		if err != nil {
			internalError(c, "failed to save note sync binding")
			return
		}

		success(c, model.SaveNoteSyncBindingResponse{
			Binding:       savedBinding,
			Target:        savedTarget,
			ChangedTarget: changedTarget,
		})
	}
}

func DeleteNoteSyncBinding(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		noteID := strings.TrimSpace(c.Param("id"))
		if noteID == "" {
			badRequest(c, "note id is required")
			return
		}

		var req model.DeleteNoteSyncBindingRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "invalid sync binding delete request")
			return
		}

		err := store.Transact(c.Request.Context(), func(txStore storage.Store) error {
			syncRepo := txStore.Sync()
			if err := syncRepo.LockBindingSlot(c.Request.Context(), noteID); err != nil {
				return err
			}
			existing, err := syncRepo.GetBinding(c.Request.Context(), noteID)
			if err != nil {
				return err
			}
			if existing.TargetID != req.ExpectedTargetID || existing.UpdatedAt != req.ExpectedUpdatedAt {
				return errSyncBindingConflict
			}
			if err := tombstoneAndReleaseClaim(c.Request.Context(), syncRepo, noteID, "user_unbound"); err != nil {
				return err
			}
			if err := syncRepo.PutSuppression(c.Request.Context(), model.NoteSyncSuppression{
				NoteID:   noteID,
				TargetID: existing.TargetID,
				Reason:   "user_unbound",
			}); err != nil {
				return err
			}
			return syncRepo.DeleteBinding(c.Request.Context(), noteID)
		})
		if errors.Is(err, sql.ErrNoRows) {
			notFound(c, "sync binding not found")
			return
		}
		if errors.Is(err, errSyncBindingConflict) {
			errorResponse(c, http.StatusConflict, "sync_binding_conflict", "sync binding was changed by another request")
			return
		}
		if err != nil {
			internalError(c, "failed to delete note sync binding")
			return
		}
		noContent(c)
	}
}

func buildNoteSyncBindingResponse(ctx context.Context, syncRepo storage.SyncRepository, noteID string) (model.NoteSyncBindingResponse, error) {
	candidates, err := buildNoteSyncBindingCandidates(ctx, syncRepo, noteID)
	if err != nil {
		return model.NoteSyncBindingResponse{}, err
	}

	binding, err := syncRepo.GetBinding(ctx, noteID)
	if errors.Is(err, sql.ErrNoRows) {
		return model.NoteSyncBindingResponse{Candidates: candidates}, nil
	}
	if err != nil {
		return model.NoteSyncBindingResponse{}, err
	}

	target, err := syncRepo.GetTarget(ctx, binding.TargetID)
	if err != nil {
		return model.NoteSyncBindingResponse{}, err
	}
	state, err := syncRepo.GetState(ctx, noteID, binding.TargetID)
	if errors.Is(err, sql.ErrNoRows) {
		state = nil
	} else if err != nil {
		return model.NoteSyncBindingResponse{}, err
	}

	return model.NoteSyncBindingResponse{
		Binding:    binding,
		Target:     target,
		State:      state,
		Candidates: candidates,
	}, nil
}

func buildNoteSyncBindingCandidates(ctx context.Context, syncRepo storage.SyncRepository, noteID string) ([]model.NoteSyncBindingCandidate, error) {
	targets, err := syncRepo.ListTargets(ctx)
	if err != nil {
		return nil, err
	}
	candidates := make([]model.NoteSyncBindingCandidate, 0, len(targets))
	for _, target := range targets {
		if !target.Enabled {
			continue
		}
		var state *model.SyncState
		existingState, err := syncRepo.GetState(ctx, noteID, target.ID)
		if errors.Is(err, sql.ErrNoRows) {
			state = nil
		} else if err != nil {
			return nil, err
		} else {
			state = existingState
		}
		candidates = append(candidates, model.NoteSyncBindingCandidate{
			Target: target,
			State:  state,
		})
	}
	return candidates, nil
}

func tombstoneAndReleaseClaim(ctx context.Context, syncRepo storage.SyncRepository, noteID string, reason string) error {
	claim, err := syncRepo.GetExternalClaimByNote(ctx, noteID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := syncRepo.PutImportTombstone(ctx, model.SyncImportTombstone{
		ExternalKey:  claim.ExternalKey,
		TargetID:     claim.TargetID,
		FormerNoteID: claim.NoteID,
		ExternalType: claim.ExternalType,
		ExternalID:   claim.ExternalID,
		ExternalPath: claim.ExternalPath,
		Reason:       reason,
	}); err != nil {
		return err
	}
	return syncRepo.ReleaseExternalClaim(ctx, noteID)
}
