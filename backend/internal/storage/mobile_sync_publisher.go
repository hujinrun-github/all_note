package storage

import "context"

type MobileSyncPublisherStore interface {
	MobileSyncPublisher() MobileSyncPublisherRepository
}

func MobileSyncPublisherRepositoryFrom(store Store) (MobileSyncPublisherRepository, error) {
	publisherStore, ok := store.(MobileSyncPublisherStore)
	if !ok || publisherStore.MobileSyncPublisher() == nil {
		return nil, ErrMobileSyncStorage
	}
	return publisherStore.MobileSyncPublisher(), nil
}

type MobileSyncPublisherRepository interface {
	PublishNextWorkspace(context.Context, int, int64) (int, error)
	PruneExpired(context.Context, int64) (int, error)
}
