import { useState } from 'react';
import type { FormEvent } from 'react';
import { QRCodeSVG } from 'qrcode.react';
import { api, type PasskeyEntry } from '../../api/client';
import { registerPasskey } from '../../utils/webauthn';
import {
  Section, SubHeading, Field, SelectField, Chip,
  LoadingState, ErrorState, EmptyState, SectionBoundary, useConfirm,
} from '../../components/ui';
import { fmtDate, labelFor } from '../../utils/format';
import { useLoad, errorText, InlineError, RowList, ROW_BTN, ROW_BTN_DANGER, FORM_CARD, type LoadResult } from './shared';

interface User {
  id: string;
  username: string;
  role: string;
  is_active: boolean;
  created_at: string;
  totp_enabled: boolean;
}

type UsersLoad = LoadResult<{ users: User[] }>;

const BTN = 'min-h-[44px] md:min-h-[40px]';
const digits6 = (v: string) => v.replace(/\D/g, '').slice(0, 6);

/* ───────────────────────────── Passkeys ───────────────────────────── */

function PasskeysSection() {
  const supported = typeof window !== 'undefined' && !!window.PublicKeyCredential;
  const { data, loading, error, reload } = useLoad(() => api.listPasskeys());
  const [confirm, confirmDialog] = useConfirm();
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState('');
  const [registering, setRegistering] = useState(false);
  const [formError, setFormError] = useState('');
  const [actionError, setActionError] = useState('');
  const passkeys: PasskeyEntry[] = data?.passkeys ?? [];

  const register = async (e: FormEvent) => {
    e.preventDefault();
    setFormError('');
    setRegistering(true);
    try {
      await registerPasskey(name.trim() || undefined);
      setName('');
      setAdding(false);
      await reload();
    } catch (err) {
      setFormError(errorText(err, 'The passkey couldn’t be registered.'));
    } finally {
      setRegistering(false);
    }
  };

  const remove = async (pk: PasskeyEntry) => {
    const ok = await confirm({
      title: `Remove passkey “${pk.name}”?`,
      body: 'You will no longer be able to sign in with this passkey. Your password and other passkeys keep working. This can’t be undone.',
      confirmLabel: 'Remove passkey',
    });
    if (!ok) return;
    setActionError('');
    try {
      await api.deletePasskey(pk.id);
      await reload();
    } catch (err) {
      setActionError(errorText(err, 'The passkey couldn’t be removed.'));
    }
  };

  return (
    <Section
      title="Passkeys"
      subtitle="Sign in with your device’s fingerprint, face or PIN instead of a password."
      first
      actions={supported && !adding ? (
        <button type="button" onClick={() => { setFormError(''); setAdding(true); }} className={`apple-btn-secondary ${BTN}`}>Add passkey</button>
      ) : undefined}
    >
      {!supported && (
        <p className="mb-4 text-[14px] text-ink-body">
          This browser can’t register passkeys here. Passkeys need a secure (HTTPS) connection.
        </p>
      )}
      {adding && (
        <form onSubmit={register} noValidate className={`${FORM_CARD} mb-4`}>
          <SubHeading>Add Passkey</SubHeading>
          <Field label="Passkey name" hint="Optional. Name the device so you can recognise it later, e.g. “Laptop”." value={name} onChange={e => setName(e.target.value)} autoFocus />
          <InlineError>{formError}</InlineError>
          <div className="flex flex-wrap justify-end gap-2.5">
            <button type="button" onClick={() => { setAdding(false); setFormError(''); }} className={`apple-btn-secondary ${BTN}`}>Cancel</button>
            <button type="submit" disabled={registering} className={`apple-btn-primary ${BTN}`}>{registering ? 'Waiting for your device…' : 'Register passkey'}</button>
          </div>
        </form>
      )}
      <InlineError>{actionError}</InlineError>
      {loading ? <LoadingState lines={2} /> : error && !data ? (
        <ErrorState message="The passkeys couldn’t be loaded." details={error} onRetry={reload} />
      ) : passkeys.length === 0 ? (
        !adding && (
          <EmptyState
            title="No passkeys yet"
            message="Add a passkey to sign in without typing your password."
            action={supported ? { label: 'Add passkey', onClick: () => setAdding(true) } : undefined}
          />
        )
      ) : (
        <RowList>
          {passkeys.map(pk => (
            <li key={pk.id} className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 px-4 py-3">
              <div className="min-w-0 flex-1 basis-48">
                <p className="text-[15px] font-medium text-ink break-words">{pk.name}</p>
                <p className="text-[13px] text-ink-muted mt-0.5">Added {fmtDate(pk.created_at)}</p>
              </div>
              <button type="button" onClick={() => remove(pk)} aria-label={`Remove ${pk.name}…`} className={ROW_BTN_DANGER}>Remove…</button>
            </li>
          ))}
        </RowList>
      )}
      {confirmDialog}
    </Section>
  );
}

/* ─────────────────────── Two-factor authentication ─────────────────────── */

function TotpSetup({ user, setup, onDone, onCancel }: { user: User; setup: { url: string; secret: string }; onDone: () => void; onCancel: () => void }) {
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (code.length !== 6) { setError('Enter the 6-digit code from your authenticator app'); return; }
    setError('');
    setSaving(true);
    try {
      await api.verifyTOTP(user.id, code);
      onDone();
    } catch (err) {
      setError(errorText(err, 'That code wasn’t accepted. Try the next one.'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <form onSubmit={submit} noValidate className={`${FORM_CARD} mt-4`}>
      <SubHeading>Enable two-factor authentication for {user.username}</SubHeading>
      <p className="text-[14px] text-ink-body">
        Scan the QR code with an authenticator app, then enter the 6-digit code it shows.
      </p>
      <div className="flex flex-col items-center gap-3">
        <div className="rounded-[6px] bg-white p-3">
          <QRCodeSVG value={setup.url} size={192} />
        </div>
        <p className="text-[13px] text-ink-muted text-center max-w-xs">
          Can’t scan? Enter this key instead:<br />
          <span className="font-mono break-all text-ink">{setup.secret}</span>
        </p>
      </div>
      <Field
        label="6-digit code"
        inputMode="numeric"
        autoComplete="one-time-code"
        maxLength={6}
        value={code}
        onChange={e => setCode(digits6(e.target.value))}
        error={error || undefined}
        autoFocus
      />
      <div className="flex flex-wrap justify-end gap-2.5">
        <button type="button" onClick={onCancel} className={`apple-btn-secondary ${BTN}`}>Cancel</button>
        <button type="submit" disabled={saving} className={`apple-btn-primary ${BTN}`}>{saving ? 'Checking…' : 'Verify and enable'}</button>
      </div>
    </form>
  );
}

function TotpDisable({ user, onDone, onCancel }: { user: User; onDone: () => void; onCancel: () => void }) {
  const [confirm, confirmDialog] = useConfirm();
  const [code, setCode] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (code.length !== 6 && !password) {
      setError('Enter a 6-digit code or your current password');
      return;
    }
    setError('');
    const ok = await confirm({
      title: `Disable two-factor authentication for “${user.username}”?`,
      body: 'Signing in will then need only the password. You can enable it again at any time, but it has to be set up from scratch with a new QR code.',
      confirmLabel: 'Disable 2FA',
    });
    if (!ok) return;
    setSaving(true);
    try {
      await api.disableTOTP(user.id, { code: code || undefined, password: password || undefined });
      onDone();
    } catch (err) {
      setError(errorText(err, 'Two-factor authentication couldn’t be disabled.'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <form onSubmit={submit} noValidate className={`${FORM_CARD} mt-4`}>
      <SubHeading>Disable two-factor authentication for {user.username}</SubHeading>
      <p className="text-[14px] text-ink-body">To confirm it’s you, enter a current authenticator code or your password.</p>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <Field label="6-digit code" inputMode="numeric" autoComplete="one-time-code" maxLength={6} value={code} onChange={e => setCode(digits6(e.target.value))} autoFocus />
        <Field label="Or current password" type="password" autoComplete="current-password" value={password} onChange={e => setPassword(e.target.value)} />
      </div>
      <InlineError>{error}</InlineError>
      <div className="flex flex-wrap justify-end gap-2.5">
        <button type="button" onClick={onCancel} className={`apple-btn-secondary ${BTN}`}>Cancel</button>
        <button type="submit" disabled={saving} className={`apple-btn-danger ${BTN}`}>{saving ? 'Disabling…' : 'Disable 2FA…'}</button>
      </div>
      {confirmDialog}
    </form>
  );
}

function TwoFactorSection({ load }: { load: UsersLoad }) {
  const { data, loading, error, reload } = load;
  const users = data?.users ?? [];
  const [setup, setSetup] = useState<{ userId: string; url: string; secret: string } | null>(null);
  const [disableId, setDisableId] = useState<string | null>(null);
  const [actionError, setActionError] = useState('');

  const startSetup = async (u: User) => {
    setActionError('');
    setDisableId(null);
    try {
      const r = await api.setupTOTP(u.id);
      setSetup({ userId: u.id, url: r.url, secret: r.secret });
    } catch (err) {
      setActionError(errorText(err, 'Two-factor setup couldn’t be started.'));
    }
  };

  const setupUser = setup && users.find(u => u.id === setup.userId);
  const disableUser = disableId && users.find(u => u.id === disableId);

  return (
    <Section title="Two-Factor Authentication" subtitle="A 6-digit code from an authenticator app, asked for at every password sign-in.">
      {loading ? <LoadingState lines={2} /> : error && !data ? (
        <ErrorState message="The two-factor status couldn’t be loaded." details={error} onRetry={reload} />
      ) : users.length === 0 ? (
        <p className="text-[14px] text-ink-body">Two-factor authentication becomes available once a user account exists.</p>
      ) : (
        <>
          <RowList>
            {users.map(u => (
              <li key={u.id} className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 px-4 py-3">
                <div className="min-w-0 flex-1 basis-48">
                  <p className="flex flex-wrap items-center gap-2 text-[15px] font-medium text-ink">
                    <span className="break-words">{u.username}</span>
                    <Chip tone={u.totp_enabled ? 'success' : 'neutral'}>{u.totp_enabled ? 'Enabled' : 'Disabled'}</Chip>
                  </p>
                  <p className="text-[13px] text-ink-muted mt-0.5">
                    {u.totp_enabled ? 'Authenticator app required at every password sign-in.' : 'Signs in with the password alone.'}
                  </p>
                </div>
                {u.totp_enabled ? (
                  <button type="button" onClick={() => { setActionError(''); setSetup(null); setDisableId(u.id); }} aria-label={`Disable two-factor authentication for ${u.username}…`} className={ROW_BTN_DANGER}>Disable…</button>
                ) : (
                  <button type="button" onClick={() => startSetup(u)} aria-label={`Enable two-factor authentication for ${u.username}`} className={ROW_BTN}>Enable</button>
                )}
              </li>
            ))}
          </RowList>
          <div className="mt-3"><InlineError>{actionError}</InlineError></div>
          {setup && setupUser && (
            <TotpSetup
              key={setup.secret}
              user={setupUser}
              setup={setup}
              onCancel={() => setSetup(null)}
              onDone={() => { setSetup(null); reload(); }}
            />
          )}
          {disableUser && (
            <TotpDisable
              key={disableUser.id}
              user={disableUser}
              onCancel={() => setDisableId(null)}
              onDone={() => { setDisableId(null); reload(); }}
            />
          )}
        </>
      )}
    </Section>
  );
}

/* ───────────────────────────── Users ───────────────────────────── */

function AddUserForm({ onDone, onCancel }: { onDone: () => void; onCancel: () => void }) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [role, setRole] = useState('member');
  const [submitted, setSubmitted] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  const userErr = !username.trim() ? 'Enter a username' : undefined;
  const passErr = password.length < 8 ? 'Use at least 8 characters' : undefined;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setSubmitted(true);
    setError('');
    if (userErr || passErr) return;
    setSaving(true);
    try {
      await api.createUser({ username: username.trim(), password, role });
      onDone();
    } catch (err) {
      setError(errorText(err, 'The user couldn’t be created.'));
    } finally {
      setSaving(false);
    }
  };

  return (
    <form onSubmit={submit} noValidate className={`${FORM_CARD} mb-4`}>
      <SubHeading>Add User</SubHeading>
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <Field label="Username" autoComplete="off" spellCheck={false} value={username} onChange={e => setUsername(e.target.value)} error={submitted ? userErr : undefined} autoFocus />
        <Field label="Password" type="password" autoComplete="new-password" hint="At least 8 characters." value={password} onChange={e => setPassword(e.target.value)} error={submitted ? passErr : undefined} />
        <SelectField label="Role" value={role} onChange={e => setRole(e.target.value)}>
          <option value="member">{labelFor('member')}</option>
          <option value="admin">{labelFor('admin')}</option>
        </SelectField>
      </div>
      <InlineError>{error}</InlineError>
      <div className="flex flex-wrap justify-end gap-2.5">
        <button type="button" onClick={onCancel} className={`apple-btn-secondary ${BTN}`}>Cancel</button>
        <button type="submit" disabled={saving} className={`apple-btn-primary ${BTN}`}>{saving ? 'Adding…' : 'Add user'}</button>
      </div>
    </form>
  );
}

function UsersSection({ load }: { load: UsersLoad }) {
  const { data, loading, error, reload } = load;
  const users = data?.users ?? [];
  const [confirm, confirmDialog] = useConfirm();
  const [adding, setAdding] = useState(false);
  const [actionError, setActionError] = useState('');

  const activeAdmins = users.filter(u => u.role === 'admin' && u.is_active).length;
  const isOnlyAdmin = (u: User) => u.role === 'admin' && u.is_active && activeAdmins <= 1;

  const remove = async (u: User) => {
    const ok = await confirm({
      title: `Delete user “${u.username}”?`,
      body: 'Their sign-in, passkeys and sessions are removed immediately. Imported data stays. This can’t be undone.',
      confirmLabel: 'Delete user',
    });
    if (!ok) return;
    setActionError('');
    try {
      await api.deleteUser(u.id);
      await reload();
    } catch (err) {
      setActionError(errorText(err, `“${u.username}” couldn’t be deleted.`));
    }
  };

  const toggle = async (u: User) => {
    if (u.is_active) {
      const ok = await confirm({
        title: `Disable user “${u.username}”?`,
        body: 'They can’t sign in until the account is enabled again. Nothing is deleted.',
        confirmLabel: 'Disable user',
      });
      if (!ok) return;
    }
    setActionError('');
    try {
      await api.toggleUser(u.id);
      await reload();
    } catch (err) {
      setActionError(errorText(err, `“${u.username}” couldn’t be updated.`));
    }
  };

  return (
    <Section
      title="Users"
      actions={!adding ? <button type="button" onClick={() => setAdding(true)} className={`apple-btn-secondary ${BTN}`}>Add user</button> : undefined}
    >
      {adding && <AddUserForm onCancel={() => setAdding(false)} onDone={() => { setAdding(false); reload(); }} />}
      <InlineError>{actionError}</InlineError>
      {loading ? <LoadingState lines={3} /> : error && !data ? (
        <ErrorState message="The users couldn’t be loaded." details={error} onRetry={reload} />
      ) : users.length === 0 ? (
        !adding && (
          <EmptyState
            title="No users yet"
            message="Add a user to give each person their own sign-in."
            action={{ label: 'Add user', onClick: () => setAdding(true) }}
          />
        )
      ) : (
        <>
          {/* Desktop: table */}
          <div className="hidden md:block rounded-apple border border-divider overflow-hidden">
            <table className="apple-table">
              <thead>
                <tr>
                  <th scope="col">User</th>
                  <th scope="col">Role</th>
                  <th scope="col">Status</th>
                  <th scope="col">Added</th>
                  <th scope="col"><span className="sr-only">Actions</span></th>
                </tr>
              </thead>
              <tbody>
                {users.map(u => {
                  const locked = isOnlyAdmin(u);
                  return (
                    <tr key={u.id}>
                      <td className="font-medium text-ink">{u.username}</td>
                      <td>{labelFor(u.role)}</td>
                      <td>
                        <span className="flex flex-wrap gap-1.5">
                          <Chip tone={u.is_active ? 'success' : 'neutral'}>{u.is_active ? 'Active' : 'Disabled'}</Chip>
                          {u.totp_enabled && <Chip>2FA</Chip>}
                        </span>
                      </td>
                      <td>{fmtDate(u.created_at)}</td>
                      <td>
                        <UserActions u={u} locked={locked} onToggle={toggle} onRemove={remove} className="justify-end" />
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          {/* Phones: cards */}
          <ul className="md:hidden rounded-apple border border-divider divide-y divide-divider">
            {users.map(u => (
              <li key={u.id} className="px-4 py-3 flex flex-col gap-2.5">
                <div>
                  <p className="flex flex-wrap items-center gap-2 text-[15px] font-medium text-ink">
                    <span className="break-words">{u.username}</span>
                    <Chip tone={u.is_active ? 'success' : 'neutral'}>{u.is_active ? 'Active' : 'Disabled'}</Chip>
                    {u.totp_enabled && <Chip>2FA</Chip>}
                  </p>
                  <p className="text-[13px] text-ink-muted mt-0.5">{labelFor(u.role)} · added {fmtDate(u.created_at)}</p>
                </div>
                <UserActions u={u} locked={isOnlyAdmin(u)} onToggle={toggle} onRemove={remove} />
              </li>
            ))}
          </ul>
        </>
      )}
      {confirmDialog}
    </Section>
  );
}

function UserActions({ u, locked, onToggle, onRemove, className = '' }: { u: User; locked: boolean; onToggle: (u: User) => void; onRemove: (u: User) => void; className?: string }) {
  const reasonId = `only-admin-${u.id}`;
  return (
    <div className={`flex flex-wrap items-center gap-2 ${className}`}>
      {locked && <span id={reasonId} className="text-[12px] text-ink-muted">The only administrator can’t be deleted</span>}
      {!locked && (u.is_active ? (
        <button type="button" onClick={() => onToggle(u)} aria-label={`Disable ${u.username}…`} className={ROW_BTN_DANGER}>Disable…</button>
      ) : (
        <button type="button" onClick={() => onToggle(u)} aria-label={`Enable ${u.username}`} className={ROW_BTN}>Enable</button>
      ))}
      <button
        type="button"
        onClick={() => onRemove(u)}
        disabled={locked}
        aria-describedby={locked ? reasonId : undefined}
        aria-label={`Delete ${u.username}…`}
        className={ROW_BTN_DANGER}
      >
        Delete…
      </button>
    </div>
  );
}

export default function SecurityTab({ authEnabled = true }: { authEnabled?: boolean }) {
  const users = useLoad(() => api.listUsers());
  return (
    <>
      {authEnabled ? (
        <>
          <SectionBoundary><PasskeysSection /></SectionBoundary>
          <SectionBoundary><TwoFactorSection load={users} /></SectionBoundary>
        </>
      ) : (
        <Section title="Sign-in" first>
          <EmptyState
            title="Sign-in is switched off"
            message="This instance runs without a password, so passkeys and two-factor authentication don’t apply. Set ADMIN_PASSWORD on the server to require sign-in."
          />
        </Section>
      )}
      <SectionBoundary><UsersSection load={users} /></SectionBoundary>
    </>
  );
}
