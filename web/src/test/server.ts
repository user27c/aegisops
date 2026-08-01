import { setupServer } from 'msw/node'
import { handlers } from './handlers'

/** MSW server：测试中拦截全部 /api/v1 请求。 */
export const server = setupServer(...handlers)
