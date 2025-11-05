import { test } from 'node:test';
import assert from 'node:assert/strict';

import { listLogs } from '../lib/api';

test('listLogs forwards pagination params and normalizes payload', async () => {
  const calls: Array<{ url: string; init: RequestInit }> = [];

  const mockEntries = [
    {
      ID: 1,
      ProjectID: 'project-123',
      Source: 'builder',
      Level: 'info',
      Message: 'build started',
      Metadata: '{"stage":"build"}',
      CreatedAt: '2024-01-02T03:04:05Z',
    },
  ];

  const originalFetch = global.fetch;
  global.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = input instanceof URL ? input.toString() : String(input);
    calls.push({ url, init: init ?? {} });
    return new Response(JSON.stringify(mockEntries), { status: 200 });
  };

  try {
    const result = await listLogs('token-abc', 'project-123', 25, 10);
    assert.equal(result.length, 1);
    const [entry] = result;
    assert.equal(entry.project_id, 'project-123');
    assert.deepEqual(entry.metadata, { stage: 'build' });

    assert.equal(calls.length, 1);
    const call = calls[0];
    assert.equal(call.url, 'http://localhost:4000/logs/project-123?limit=25&offset=10');
    assert.ok(call.init?.headers);
    const headers = call.init.headers as Record<string, string>;
    assert.equal(headers.Authorization, 'Bearer token-abc');
    assert.equal(call.init.method, 'GET');
  } finally {
    global.fetch = originalFetch;
  }
});
