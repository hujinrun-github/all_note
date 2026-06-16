package repository

import (
	"sync"

	"github.com/hujinrun/flowspace/internal/storage"
)

var (
	activeStoreMu sync.RWMutex
	activeStore   storage.Store
)

func SetStore(store storage.Store) {
	activeStoreMu.Lock()
	defer activeStoreMu.Unlock()
	activeStore = store
}

func ActiveStore() storage.Store {
	activeStoreMu.RLock()
	defer activeStoreMu.RUnlock()
	if activeStore == nil {
		panic("repository active storage store is not initialized")
	}
	return activeStore
}
