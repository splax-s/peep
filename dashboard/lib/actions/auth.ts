'use server';

import { revalidatePath } from 'next/cache';
import { ApiError, login, signup } from '@/lib/api';
import { clearSession, setSession } from '@/lib/session';

export type AuthMode = 'login' | 'signup';

export interface AuthInput {
  mode: AuthMode;
  email: string;
  password: string;
}

export interface AuthResult {
  success: boolean;
  error?: string;
}

export async function authenticate(input: AuthInput): Promise<AuthResult> {
  const email = input.email.trim().toLowerCase();
  const password = input.password.trim();

  if (!email || !password) {
    return { success: false, error: 'Email and password are required.' };
  }

  try {
    const session = input.mode === 'signup' ? await signup(email, password) : await login(email, password);
    await setSession(session);
    revalidatePath('/');
    return { success: true };
  } catch (error) {
    if (error instanceof ApiError) {
      return { success: false, error: error.message };
    }
    if (error instanceof Error) {
      return { success: false, error: error.message };
    }
    return { success: false, error: 'Authentication failed.' };
  }
}

export async function logout(): Promise<void> {
  await clearSession();
  revalidatePath('/');
}
