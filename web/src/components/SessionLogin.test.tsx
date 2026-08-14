import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import SessionLogin from './SessionLogin'

describe('SessionLogin', () => {
  it('stores the token in sessionStorage and continues', async () => {
    const user = userEvent.setup()
    let authenticated = false
    render(<SessionLogin onAuthenticated={() => { authenticated = true }} />)
    await user.type(screen.getByLabelText('Access Token'), 'portfolio-token')
    await user.click(screen.getByRole('button', { name: '进入控制台' }))
    expect(sessionStorage.getItem('aegisops_token')).toBe('portfolio-token')
    expect(authenticated).toBe(true)
  })
})
