package main

import (
	"context"
	"errors"

	"github.com/hujinrun/flowspace/internal/airuntime"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/planstore"
	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/transcription"
	"github.com/hujinrun/flowspace/internal/webplanning"
)

type v2PlanningWorkspaces struct {
	source webplanning.WorkspaceLister
	models taskapp.ModelSelector
}

// Stable legacy workspaces have no planning jobs. Exclude them from the
// worker scan without interpreting transitional or unavailable state as v1.
func (s v2PlanningWorkspaces) ListActiveWorkspaceIDs(ctx context.Context) ([]string, error) {
	ids, err := s.source.ListActiveWorkspaceIDs(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(ids))
	var failures error
	for _, id := range ids {
		model, err := s.models.SelectTaskDomainModel(auth.ContextWithWorkspaceScope(ctx, id), id)
		if err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		if model == taskapp.ModelV2 {
			result = append(result, id)
		}
	}
	return result, failures
}

func newWebPlanningService(runtime planstore.RuntimeResolver, objects objectstore.Store, transcriber transcription.Transcriber, chat *airuntime.Generator) (*webplanning.Service, error) {
	repository, err := planstore.New(runtime)
	if err != nil {
		return nil, err
	}
	bounded := transcription.NewWebAudioTranscriber(transcriber)
	config := webplanning.Config{
		Repository: repository, Objects: objects, Transcriber: bounded,
		ResolveTranscription: func(ctx context.Context, workspaceID string) error {
			if err := bounded.CheckAvailable(ctx, workspaceID); err != nil {
				return err
			}
			if objects == nil {
				return objectstore.ErrUnavailable
			}
			if checker, ok := objects.(interface{ CheckAvailable(context.Context) error }); ok {
				return checker.CheckAvailable(ctx)
			}
			return nil
		},
	}
	if chat != nil {
		config.Chat, config.Features, config.ResolveGeneration = chat, chat, chat.CheckAvailable
	}
	return webplanning.New(config)
}
