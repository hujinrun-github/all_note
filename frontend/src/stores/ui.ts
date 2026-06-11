import { create } from 'zustand'

interface UIState {
  captureOpen: boolean
  setCaptureOpen: (open: boolean) => void
}

export const useUIStore = create<UIState>((set) => ({
  captureOpen: false,
  setCaptureOpen: (open) => set({ captureOpen: open }),
}))
