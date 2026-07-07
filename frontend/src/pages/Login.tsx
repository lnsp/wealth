import { useState, useEffect, type FormEvent } from 'react';
import { loginWithPasskey } from '../utils/webauthn';

interface Props {
  onLogin: () => void;
}

export default function Login({ onLogin }: Props) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
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
      const resp = await fetch('/api/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username, password }),
      });
      if (!resp.ok) {
        setError('Invalid password');
        return;
      }
      onLogin();
    } catch {
      setError('Connection failed');
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
      setError(e instanceof Error ? e.message : 'Passkey login failed');
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
          <span className="font-serif text-[14px] tracking-[0.12em] uppercase text-ink-muted" style={{ fontVariantCaps: 'small-caps' }}>Wealth</span>
        </div>

        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <input
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="Username"
              autoFocus
              className="w-full border-0 border-b border-divider bg-transparent px-0 py-3 text-[16px] text-ink placeholder:font-serif placeholder:italic placeholder:text-ink-muted/50 focus:outline-none focus:border-forest transition-colors duration-[250ms]"
            />
          </div>
          <div>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="Password"
              className="w-full border-0 border-b border-divider bg-transparent px-0 py-3 text-[16px] text-ink placeholder:font-serif placeholder:italic placeholder:text-ink-muted/50 focus:outline-none focus:border-forest transition-colors duration-[250ms]"
            />
          </div>

          {error && (
            <p className="text-[13px] text-claret mt-2">{error}</p>
          )}

          <button
            type="submit"
            disabled={loading || !password}
            className="w-full mt-6 apple-btn-primary py-3"
          >
            {loading ? 'Signing in...' : 'Sign In'}
          </button>
        </form>

        {passkeyAvailable && (
          <>
            <div className="flex items-center gap-3 mt-6 mb-4">
              <div className="flex-1 border-t border-divider" />
              <span className="text-[12px] text-ink-muted">or</span>
              <div className="flex-1 border-t border-divider" />
            </div>
            <button
              onClick={handlePasskeyLogin}
              disabled={loading}
              className="w-full apple-btn-secondary py-3 flex items-center justify-center gap-2"
            >
              <svg className="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
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
