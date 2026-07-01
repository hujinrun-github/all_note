package repository

import (
	"bytes"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/hujinrun/flowspace/internal/storage"
)

var (
	activeStoreMu     sync.RWMutex
	activeStore       storage.Store
	scopedStores      sync.Map
	scopedStoreOwners atomic.Int64
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

func WithScopedStore(store storage.Store, fn func()) {
	if store == nil {
		fn()
		return
	}
	owner := currentGoroutineID()
	previous, hadPrevious := scopedStores.Load(owner)
	scopedStores.Store(owner, store)
	scopedStoreOwners.Add(1)
	defer func() {
		if hadPrevious {
			scopedStores.Store(owner, previous)
		} else {
			scopedStores.Delete(owner)
		}
		scopedStoreOwners.Add(-1)
	}()
	fn()
}

func CurrentStore() storage.Store {
	if scopedStoreOwners.Load() > 0 {
		if store, ok := scopedStores.Load(currentGoroutineID()); ok {
			return store.(storage.Store)
		}
	}
	activeStoreMu.RLock()
	defer activeStoreMu.RUnlock()
	return activeStore
}

func currentGoroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	line := bytes.TrimPrefix(buf[:n], []byte("goroutine "))
	idEnd := bytes.IndexByte(line, ' ')
	if idEnd < 0 {
		panic("repository scoped store: failed to parse goroutine id")
	}
	id, err := strconv.ParseUint(string(line[:idEnd]), 10, 64)
	if err != nil {
		panic("repository scoped store: failed to parse goroutine id")
	}
	return id
}
