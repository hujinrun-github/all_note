import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import { QuickCapture } from './QuickCapture'

vi.mock('./QuickCaptureV2', () => ({
  QuickCaptureV2: () => <div>V2 快速捕获</div>,
}))

describe('QuickCapture', () => {
  it('always renders the V2 quick capture flow', () => {
    render(<QuickCapture />)

    expect(screen.getByText('V2 快速捕获')).toBeVisible()
  })
})
