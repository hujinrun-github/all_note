package main

import (
	"context"
	"fmt"

	"github.com/hujinrun/flowspace/internal/config"
	"github.com/hujinrun/flowspace/internal/credentials"
	"github.com/hujinrun/flowspace/internal/japaneseintegration"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskruntime"
)

func newJapaneseIntegrationService(control storage.Store, driver storage.Driver, runtimes *taskruntime.Resolver) (*japaneseintegration.Service, error) {
	sqlStore, ok := control.(storage.SQLStore)
	if !ok {
		return nil, fmt.Errorf("integration control store does not expose SQL database")
	}
	cfg, err := config.LoadCredentialsConfig()
	if err != nil {
		return nil, err
	}
	keyring, err := credentials.LoadKeyringFile(cfg.KeyringFile, cfg.ActiveKeyID)
	if err != nil {
		return nil, err
	}
	return japaneseintegration.New(japaneseintegration.Config{
		ControlDB: sqlStore.SQLDB(), ControlDriver: driver, Keyring: keyring,
		Runtimes: japaneseintegration.MobileRuntimeResolverFunc(func(ctx context.Context, workspace string) (japaneseintegration.RuntimeSnapshot, error) {
			snapshot, err := runtimes.ResolveMobileRuntime(ctx, workspace)
			if err != nil {
				return japaneseintegration.RuntimeSnapshot{}, err
			}
			return japaneseintegration.RuntimeSnapshot{WorkspaceID: snapshot.WorkspaceID, Epoch: snapshot.Epoch, Driver: snapshot.Driver, DB: snapshot.DB, Writer: snapshot.Writer, Application: snapshot.Application}, nil
		}),
		Authorize: func(ctx context.Context, workspace, actor string) error {
			user, err := control.Auth().GetUserByID(ctx, actor)
			if err != nil || user.Status != "active" {
				return japaneseintegration.ErrUnauthorized
			}
			member, err := control.Auth().GetWorkspaceMembership(ctx, workspace, actor)
			if err != nil || (member.Role != "owner" && member.Role != "admin") {
				return japaneseintegration.ErrUnauthorized
			}
			return nil
		},
	})
}
