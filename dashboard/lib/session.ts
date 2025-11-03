import { cookies } from 'next/headers';
import type { SessionPayload } from '@/types';

const SESSION_COOKIE = 'peep-session';

function encodeSession(session: SessionPayload): string {
  return Buffer.from(JSON.stringify(session), 'utf8').toString('base64url');
}

function decodeSession(value: string): SessionPayload | null {
  try {
    const json = Buffer.from(value, 'base64url').toString('utf8');
    return JSON.parse(json) as SessionPayload;
  } catch {
    return null;
  }
}

export async function getSession(): Promise<SessionPayload | null> {
  const cookieStore = await cookies();
  const raw = cookieStore.get(SESSION_COOKIE)?.value;
  if (!raw) {
    return null;
  }
  return decodeSession(raw);
}

export async function setSession(session: SessionPayload): Promise<void> {
  const cookieStore = await cookies();
  const value = encodeSession(session);
  const maxAge = Math.max(session.tokens.ExpiresIn, 60);
  cookieStore.set(SESSION_COOKIE, value, {
    httpOnly: true,
    secure: process.env.NODE_ENV === 'production',
    sameSite: 'lax',
    path: '/',
    maxAge,
  });
}

export async function clearSession(): Promise<void> {
  const cookieStore = await cookies();
  cookieStore.delete(SESSION_COOKIE);
}
