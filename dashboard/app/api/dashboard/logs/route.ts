'use server';

import { NextResponse } from 'next/server';
import { ApiError, listLogs } from '@/lib/api';
import { LOGS_PAGE_SIZE } from '@/lib/dashboard';
import { clearSession, getSession } from '@/lib/session';

function parsePositiveInt(value: string | null, fallback: number): number {
  if (!value) {
    return fallback;
  }
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return fallback;
  }
  return Math.floor(parsed);
}

function parseNonNegativeInt(value: string | null, fallback: number): number {
  if (!value) {
    return fallback;
  }
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed < 0) {
    return fallback;
  }
  return Math.floor(parsed);
}

export async function GET(request: Request) {
  const session = await getSession();
  if (!session) {
    return NextResponse.json({ error: 'Not authenticated.' }, { status: 401 });
  }

  const url = new URL(request.url);
  const projectId = url.searchParams.get('project');
  if (!projectId) {
    return NextResponse.json({ error: 'Project identifier is required.' }, { status: 400 });
  }

  const limit = parsePositiveInt(url.searchParams.get('limit'), LOGS_PAGE_SIZE);
  const offset = parseNonNegativeInt(url.searchParams.get('offset'), 0);

  try {
    const entries = await listLogs(session.tokens.AccessToken, projectId, limit, offset);
    const hasMore = entries.length === limit;
    return NextResponse.json(
      { entries, hasMore },
      {
        status: 200,
        headers: {
          'Cache-Control': 'no-store',
        },
      },
    );
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      await clearSession();
      return NextResponse.json({ error: 'Not authenticated.' }, { status: 401 });
    }
    const message = error instanceof Error ? error.message : 'Failed to fetch logs.';
    return NextResponse.json({ error: message }, { status: 502 });
  }
}
