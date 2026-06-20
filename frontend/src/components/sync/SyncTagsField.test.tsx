import { useState } from 'react'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { SyncTagsField } from './SyncTagsField'

describe('SyncTagsField', () => {
  it('adds tags directly inside the tag input box', async () => {
    const user = userEvent.setup()
    render(<StatefulSyncTagsField />)

    await user.type(screen.getByLabelText('添加同步标签'), 'sync, publish, #work{enter}')

    const tagInputBox = screen.getByRole('group', { name: '同步标签输入框' })
    expect(within(tagInputBox).getByText('#sync')).toBeVisible()
    expect(within(tagInputBox).getByText('#publish')).toBeVisible()
    expect(within(tagInputBox).getByText('#work')).toBeVisible()
    expect(within(tagInputBox).getByLabelText('添加同步标签')).toHaveValue('')
  })
})

function StatefulSyncTagsField() {
  const [value, setValue] = useState('')
  return <SyncTagsField value={value} onChange={setValue} />
}
