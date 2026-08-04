import { test, expect, Page } from '@playwright/test'

const INCIDENT = {
  metadata: {
    name: 'containeroomkilled-abc123',
    namespace: 'fault-lab',
    uid: '11111111-1111-1111-1111-111111111111',
    creationTimestamp: '2026-08-01T10:00:00Z',
  },
  spec: {
    fingerprint: 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
    cluster: 'local-k3s',
    alertName: 'ContainerOOMKilled',
    severity: 'critical',
    sourceStatus: 'firing',
    targetRef: { apiVersion: 'apps/v1', kind: 'Deployment', namespace: 'fault-lab', name: 'checkout-api' },
    startedAt: '2026-08-01T10:00:00Z',
  },
  status: {
    phase: 'AwaitingApproval',
    diagnosis: { category: 'OOMKilled', rootCause: '内存 limit 低于工作集', confidence: 0.91, evidenceIDs: ['event-1'] },
    proposal: { revision: 1, action: 'PatchResourceLimit', risk: 'medium', planDigest: 'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb' },
    evidence: { id: 'evidence-uuid-1', hash: 'sha256:abcdef' },
  },
}

async function mockApi(page: Page) {
  await page.route('**/api/v1/incidents', (route) =>
    route.fulfill({ json: { items: [INCIDENT] } }),
  )
  await page.route('**/api/v1/incidents/fault-lab/containeroomkilled-abc123', (route) =>
    route.fulfill({ json: INCIDENT }),
  )
  await page.route('**/api/v1/incidents/fault-lab/containeroomkilled-abc123/timeline', (route) =>
    route.fulfill({
      json: {
        items: [{ time: '2026-08-01T10:00:00Z', type: 'PhaseTransition', actor: 'aegisops-operator', sequence: 1, eventHash: '1a2b3c4d5e6f' }],
        source: 'audit',
      },
    }),
  )
  await page.route('**/api/v1/incidents/fault-lab/containeroomkilled-abc123/evidence', (route) =>
    route.fulfill({
      json: {
        id: 'evidence-uuid-1',
        hash: 'sha256:abcdef',
        windowStart: '2026-08-01T09:30:00Z',
        windowEnd: '2026-08-01T10:00:00Z',
        items: [{ id: 'event-1', kind: 'KubernetesEvent', source: 'k8s', timestamp: '2026-08-01T09:55:00Z', summary: 'ContainerOOMKilled 内存超限' }],
      },
    }),
  )
  await page.route('**/api/v1/incidents/fault-lab/containeroomkilled-abc123/approval', (route) =>
    route.fulfill({ status: 201, json: { approvalName: 'a-1', decision: 'Approve' } }),
  )
}

test('Dashboard → Detail → Approve → Phase 更新', async ({ page }) => {
  await mockApi(page)
  await page.goto('/')

  await expect(page.getByText('containeroomkilled-abc123')).toBeVisible()
  await page.getByRole('link', { name: /containeroomkilled-abc123/ }).click()

  await expect(page.getByRole('heading', { name: /fault-lab\/containeroomkilled-abc123/ })).toBeVisible()
  await expect(page.getByText('PatchResourceLimit')).toBeVisible()

  const evidenceRow = page.getByText('ContainerOOMKilled 内存超限')
  await expect(evidenceRow).toBeVisible()

  const timelineEntry = page.getByText('PhaseTransition')
  await expect(timelineEntry).toBeVisible()
  await expect(page.getByText('@aegisops-operator')).toBeVisible()

  await page.getByLabel('审批理由').fill('确认修复方案')
  await page.getByRole('button', { name: '批准并执行' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toBeVisible()
  await expect(dialog.getByText('PatchResourceLimit')).toBeVisible()
  await dialog.getByRole('button', { name: '确认批准' }).click()

  await expect(page.getByText('已批准，等待 Operator 执行。')).toBeVisible()
})
