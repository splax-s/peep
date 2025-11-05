import { NextResponse } from 'next/server';
import { API_BASE_URL } from '@/lib/api';
import { clearSession, getSession } from '@/lib/session';

type ErrorBody = { error: string };

export async function GET(request: Request) {
  const session = await getSession();
  if (!session) {
    return NextResponse.json({ error: 'Not authenticated.' } satisfies ErrorBody, { status: 401 });
  }

  const url = new URL(request.url);
  const projectId = url.searchParams.get('project');
  if (!projectId) {
    return NextResponse.json({ error: 'Project identifier is required.' } satisfies ErrorBody, { status: 400 });
  }

  const upstreamUrl = new URL('/logs/stream', API_BASE_URL);
  upstreamUrl.searchParams.set('project_id', projectId);

  const controller = new AbortController();
  const abortWithRequest = () => controller.abort();
  if (request.signal.aborted) {
    controller.abort();
  } else {
    request.signal.addEventListener('abort', abortWithRequest, { once: true });
  }

  try {
    const upstreamResponse = await fetch(upstreamUrl, {
      method: 'GET',
      headers: {
        Authorization: `Bearer ${session.tokens.AccessToken}`,
      },
      cache: 'no-store',
      signal: controller.signal,
    });

    if (upstreamResponse.status === 401) {
      await clearSession();
      return NextResponse.json({ error: 'Not authenticated.' } satisfies ErrorBody, { status: 401 });
    }

    if (!upstreamResponse.ok || !upstreamResponse.body) {
      let payload: ErrorBody = { error: 'Upstream log stream failed.' };
      try {
        const parsed = (await upstreamResponse.clone().json()) as Partial<ErrorBody>;
        if (parsed && typeof parsed.error === 'string') {
          payload = { error: parsed.error };
        }
      } catch {
        try {
          const text = await upstreamResponse.clone().text();
          if (text.trim()) {
            payload = { error: text.trim() };
          }
        } catch {
          // ignore parsing failure
        }
      }
      const status = upstreamResponse.ok ? 502 : upstreamResponse.status;
      return NextResponse.json(payload, { status });
    }

    return new Response(upstreamResponse.body, {
      status: 200,
      headers: {
        'Content-Type': 'text/event-stream',
        'Cache-Control': 'no-cache',
        Connection: 'keep-alive',
        'X-Accel-Buffering': 'no',
      },
    });
  } catch (error) {
    if (error instanceof Error && error.name === 'AbortError') {
      return new Response(null, { status: 499 });
    }
    return NextResponse.json({ error: 'Log stream unavailable.' } satisfies ErrorBody, { status: 502 });
  } finally {
    request.signal.removeEventListener('abort', abortWithRequest);
  }
}
