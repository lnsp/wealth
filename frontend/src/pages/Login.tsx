import { useState, useEffect, type FormEvent } from 'react';
import { Field } from '../components/ui';
import { loginWithPasskey } from '../utils/webauthn';

interface Props {
  onLogin: () => void;
}

export default function Login({ onLogin }: Props) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [totpRequired, setTotpRequired] = useState(false);
  const [totpCode, setTotpCode] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);
  const [passkeyAvailable, setPasskeyAvailable] = useState(false);

  useEffect(() => {
    // Check if WebAuthn is supported by the browser
    if (window.PublicKeyCredential) {
      setPasskeyAvailable(true);
    }
  }, []);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError('');
    setLoading(true);

    try {
      const payload = totpRequired
        ? { username, password, totp_code: totpCode }
        : { username, password };
      const resp = await fetch('/api/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      if (!resp.ok) {
        const data = await resp.json().catch(() => null);
        if (data?.totp_required) {
          if (!totpRequired) {
            setTotpRequired(true);
          } else {
            setError('That authentication code wasn’t accepted. Try the next one.');
          }
          return;
        }
        if (resp.status === 429) {
          setError('Too many sign-in attempts. Try again later.');
          return;
        }
        setError('Incorrect username or password.');
        return;
      }
      onLogin();
    } catch {
      setError('The server couldn’t be reached. Check your connection and try again.');
    } finally {
      setLoading(false);
    }
  };

  const handlePasskeyLogin = async () => {
    setError('');
    setLoading(true);
    try {
      await loginWithPasskey();
      onLogin();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Passkey sign-in failed');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="min-h-screen flex items-center justify-center bg-parchment">
      <div className="w-full max-w-sm px-8">
        {/* Brand monogram */}
        <div className="flex flex-col items-center mb-8">
          <svg viewBox="0 0 72 72" className="w-[72px] h-[72px] mb-3 text-forest" aria-hidden="true">
            <circle cx="36" cy="36" r="34" fill="none" stroke="currentColor" strokeWidth="1.5"/>
            <text x="36" y="48" textAnchor="middle" fontFamily="EB Garamond, Georgia, serif" fontSize="40" fontWeight="500" fill="currentColor">W</text>
          </svg>
          <span className="label-caps">Wealth</span>
        </div>

        <h1 className="sr-only">Sign in to Wealth</h1>
        <form onSubmit={handleSubmit} noValidate className="flex flex-col gap-4">
          <Field
            label="Username"
            name="username"
            type="text"
            autoComplete="username"
            autoCapitalize="none"
            spellCheck={false}
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoFocus
          />
          <Field
            label="Password"
            id="login-password"
            name="password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />

          {totpRequired && (
            <Field
              label="Authentication code"
              name="totp_code"
              hint="The 6-digit code from your authenticator app."
              inputMode="numeric"
              autoComplete="one-time-code"
              maxLength={6}
              value={totpCode}
              onChange={(e) => setTotpCode(e.target.value.replace(/\D/g, ''))}
            />
          )}

          <div role="alert" className="text-[13px] text-claret empty:hidden">{error}</div>

          <button
            type="submit"
            disabled={loading || !password}
            className="w-full mt-2 apple-btn-primary min-h-[44px]"
          >
            {loading ? 'Signing in…' : 'Sign In'}
          </button>
        </form>

        {passkeyAvailable && (
          <>
            <div className="flex items-center gap-3 mt-6 mb-4">
              <div className="flex-1 border-t border-divider" />
              <span className="text-[13px] text-ink-muted">or</span>
              <div className="flex-1 border-t border-divider" />
            </div>
            <button
              type="button"
              onClick={handlePasskeyLogin}
              disabled={loading}
              className="w-full apple-btn-secondary min-h-[44px] gap-2"
            >
              <svg className="w-4 h-4" aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <path strokeLinecap="round" strokeLinejoin="round" d="M15.75 5.25a3 3 0 013 3m3 0a6 6 0 01-7.029 5.912c-.563-.097-1.159.026-1.563.43L10.5 17.25H8.25v2.25H6v2.25H2.25v-2.818c0-.597.237-1.17.659-1.591l6.499-6.499c.404-.404.527-1 .43-1.563A6 6 0 1121.75 8.25z" />
              </svg>
              Sign in with Passkey
            </button>
          </>
        )}
      </div>
    </div>
  );
}
