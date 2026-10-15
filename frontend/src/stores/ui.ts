import { create } from 'zustand'

interface UIState {
  captureOpen: boolean
  setCaptureOpen: (open: boolean) => void
  notice: { message: string } | null
  showNotice: (message: string) => void
  dismissNotice: () => void
}

export const useUIStore = create<UIState>((set) => ({
  captureOpen: false,
  setCaptureOpen: (open) => set({ captureOpen: open }),
  notice: null,
  showNotice: (message) => set({ notice: { message } }),
  dismissNotice: () => set({ notice: null }),
}))
