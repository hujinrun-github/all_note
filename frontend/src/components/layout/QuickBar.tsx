import { useUIStore } from '../../stores/ui'

export function QuickBar() {
  const setCaptureOpen = useUIStore((s) => s.setCaptureOpen)

  return (
    <div className="fixed left-1/2 bottom-6 -translate-x-1/2 inline-flex items-center gap-1 bg-fs-surface border border-fs-border rounded-full shadow-hover px-1.5 py-1.5 z-50 max-[760px]:gap-0">
      <button className="border-0 bg-transparent rounded-full min-h-[34px] px-3 text-sm cursor-pointer hover:bg-fs-hover transition-colors">
        📝 笔记
      </button>
      <button className="border-0 bg-transparent rounded-full min-h-[34px] px-3 text-sm cursor-pointer hover:bg-fs-hover transition-colors">
        ✅ 任务
      </button>
      <button className="border-0 bg-transparent rounded-full min-h-[34px] px-3 text-sm cursor-pointer hover:bg-fs-hover transition-colors">
        📅 日程
      </button>
      <div className="w-px h-5 bg-fs-border mx-1" />
      <button
        onClick={() => setCaptureOpen(true)}
        className="border-0 bg-fs-accent text-white rounded-full min-h-[34px] px-4 text-sm cursor-pointer hover:bg-fs-accent-hover transition-colors font-medium"
      >
        快速捕获
      </button>
    </div>
  )
}
