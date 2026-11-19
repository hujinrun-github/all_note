import { create } from 'zustand'

interface UIState {
  captureMode: 'task' | 'plan'
  captureProjectID: string
  openPlanCapture: (projectID?: string) => void
  captureOpen: boolean
  setCaptureOpen: (open: boolean) => void
  notice: { message: string } | null
  showNotice: (message: string) => void
  dismissNotice: () => void
}

export const useUIStore = create<UIState>((set) => ({
  captureMode: 'task',
  captureProjectID: '',
  openPlanCapture: (projectID = '') =>
    set({
      captureOpen: true,
      captureMode: 'plan',
      captureProjectID: projectID,
    }),
  captureOpen: false,
  setCaptureOpen: (open) =>
    set({
      captureOpen: open,
      ...(open ? { captureMode: 'task', captureProjectID: '' } : {}),
    }),
  notice: null,
  showNotice: (message) => set({ notice: { message } }),
  dismissNotice: () => set({ notice: null }),
}))
