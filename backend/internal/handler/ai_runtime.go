package handler

import "context"

type WorkspaceChatService interface {
	Generate(context.Context, string, string, string) (string, error)
}

type workspaceTextGenerator struct {
	service     WorkspaceChatService
	workspaceID string
}

func (g workspaceTextGenerator) Generate(ctx context.Context, systemPrompt, userPrompt string) (string, error) {
	return g.service.Generate(ctx, g.workspaceID, systemPrompt, userPrompt)
}
