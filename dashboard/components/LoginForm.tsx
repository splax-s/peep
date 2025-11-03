'use client';

import { useMemo, useState, type ChangeEvent, type FormEvent } from 'react';
import { useRouter } from 'next/navigation';
import { authenticate } from '@/lib/actions/auth';

type AuthMode = 'login' | 'signup';

export function LoginForm() {
  const router = useRouter();
  const [mode, setMode] = useState<AuthMode>('login');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);

  const copy = useMemo(() => {
    if (mode === 'signup') {
      return {
        title: 'Create your peep account',
        subtitle: 'Sign up to start managing teams, projects, and runtime secrets.',
        cta: 'Create account',
        switchLabel: 'Already registered?',
        switchAction: 'Sign in',
      } as const;
    }
    return {
      title: 'Welcome back to peep',
      subtitle: 'Authenticate to orchestrate deployments and collaborate with your team.',
      cta: 'Sign in',
      switchLabel: 'Need access?',
      switchAction: 'Create account',
    } as const;
  }, [mode]);

  function resetFields() {
    setPassword('');
    setConfirmPassword('');
  }

  function toggleMode() {
    setMode((current) => (current === 'login' ? 'signup' : 'login'));
    setError(null);
    resetFields();
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setPending(true);
    setError(null);

    const emailTrimmed = email.trim().toLowerCase();
    const passwordTrimmed = password.trim();

    if (!emailTrimmed || !passwordTrimmed) {
      setError('Email and password are required.');
      setPending(false);
      return;
    }

    if (mode === 'signup') {
      if (passwordTrimmed.length < 8) {
        setError('Password must contain at least 8 characters.');
        setPending(false);
        return;
      }
      if (passwordTrimmed !== confirmPassword.trim()) {
        setError('Passwords do not match.');
        setPending(false);
        return;
      }
    }

    try {
      const result = await authenticate({ mode, email: emailTrimmed, password: passwordTrimmed });
      if (!result.success) {
        setError(result.error ?? 'Authentication failed.');
        return;
      }
      resetFields();
      router.refresh();
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="glass-card relative w-full max-w-md overflow-hidden p-8 shadow-lg">
      <div className="absolute inset-0 -z-10 bg-gradient-to-br from-cyan-500/20 via-transparent to-fuchsia-500/20" />
      <form className="space-y-6" onSubmit={handleSubmit}>
        <header className="space-y-2">
          <div className="badge">peep dashboard</div>
          <h1 className="text-2xl font-semibold text-white">{copy.title}</h1>
          <p className="text-sm text-slate-300">{copy.subtitle}</p>
        </header>

        <div className="space-y-4">
          <label className="block text-sm font-medium text-slate-200">
            Email address
            <input
              className="mt-2 w-full rounded-xl border border-slate-700/70 bg-slate-900/70 px-4 py-3 text-base text-slate-100 placeholder:text-slate-500 focus:border-cyan-400 focus:outline-none focus:ring-2 focus:ring-cyan-500/40"
              type="email"
              name="email"
              autoComplete="email"
              value={email}
              onChange={(event: ChangeEvent<HTMLInputElement>) => setEmail(event.target.value)}
              required
              disabled={pending}
            />
          </label>
          <label className="block text-sm font-medium text-slate-200">
            Password
            <input
              className="mt-2 w-full rounded-xl border border-slate-700/70 bg-slate-900/70 px-4 py-3 text-base text-slate-100 placeholder:text-slate-500 focus:border-cyan-400 focus:outline-none focus:ring-2 focus:ring-cyan-500/40"
              type="password"
              name="password"
              autoComplete={mode === 'login' ? 'current-password' : 'new-password'}
              value={password}
              onChange={(event: ChangeEvent<HTMLInputElement>) => setPassword(event.target.value)}
              required
              disabled={pending}
            />
          </label>
          {mode === 'signup' ? (
            <label className="block text-sm font-medium text-slate-200">
              Confirm password
              <input
                className="mt-2 w-full rounded-xl border border-slate-700/70 bg-slate-900/70 px-4 py-3 text-base text-slate-100 placeholder:text-slate-500 focus:border-cyan-400 focus:outline-none focus:ring-2 focus:ring-cyan-500/40"
                type="password"
                name="confirm-password"
                autoComplete="new-password"
                value={confirmPassword}
                onChange={(event: ChangeEvent<HTMLInputElement>) => setConfirmPassword(event.target.value)}
                required
                disabled={pending}
              />
            </label>
          ) : null}
        </div>

        {error ? (
          <p className="rounded-xl border border-rose-500/40 bg-rose-500/10 px-4 py-3 text-sm text-rose-200">
            {error}
          </p>
        ) : null}

        <button
          type="submit"
          disabled={pending}
          className="flex w-full items-center justify-center gap-2 rounded-xl bg-cyan-500 px-4 py-3 text-base font-semibold text-slate-950 transition hover:bg-cyan-400 focus:outline-none focus:ring-4 focus:ring-cyan-500/40 disabled:cursor-not-allowed disabled:opacity-60"
        >
          {pending ? 'Processing…' : copy.cta}
        </button>
      </form>
      <footer className="mt-6 flex items-center justify-between text-sm text-slate-300">
        <span>{copy.switchLabel}</span>
        <button
          type="button"
          onClick={toggleMode}
          className="font-medium text-cyan-300 transition hover:text-cyan-200"
          disabled={pending}
        >
          {copy.switchAction}
        </button>
      </footer>
    </div>
  );
}
