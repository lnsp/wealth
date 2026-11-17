import { useCallback, useEffect, useRef, useState } from 'react';
import type { ReactNode } from 'react';

export interface ConfirmOptions {
  title: string;
  body?: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  /** Destructive actions get the solid claret confirm button. Default true. */
  destructive?: boolean;
}

interface ConfirmDialogProps extends ConfirmOptions {
  open: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

/** Modal confirmation. Focus starts on Cancel; Escape and the scrim cancel. */
export function ConfirmDialog({ open, title, body, confirmLabel = 'Confirm', cancelLabel = 'Cancel', destructive = true, onConfirm, onCancel }: ConfirmDialogProps) {
  const cancelRef = useRef<HTMLButtonElement>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);
  const restoreRef = useRef<HTMLElement | null>(null);

  useEffect(() => {
    if (!open) return;
    restoreRef.current = document.activeElement as HTMLElement | null;
    cancelRef.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') { e.preventDefault(); onCancel(); }
      if (e.key === 'Tab') {
        // Two focusable controls: keep focus inside the dialog.
        e.preventDefault();
        (document.activeElement === cancelRef.current ? confirmRef.current : cancelRef.current)?.focus();
      }
    };
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('keydown', onKey);
      restoreRef.current?.focus?.();
    };
  }, [open, onCancel]);

  if (!open) return null;
  return (
    <div className="fixed inset-0 z-[150] flex items-center justify-center p-4" style={{ backgroundColor: 'rgba(20, 18, 16, 0.5)' }} onMouseDown={(e) => { if (e.target === e.currentTarget) onCancel(); }}>
      <div role="alertdialog" aria-modal="true" aria-labelledby="confirm-title" aria-describedby={body ? 'confirm-body' : undefined} className="w-[440px] max-w-full rounded-apple-lg border border-divider bg-parchment p-6 shadow-apple-lg flex flex-col gap-3.5">
        <h2 id="confirm-title" className="font-serif text-[22px] leading-7 font-medium text-ink">{title}</h2>
        {body && <div id="confirm-body" className="text-[14px] text-ink-body">{body}</div>}
        <div className="mt-1 flex flex-wrap justify-end gap-2.5">
          <button ref={cancelRef} type="button" className="apple-btn-secondary" onClick={onCancel}>{cancelLabel}</button>
          <button ref={confirmRef} type="button" className={destructive ? 'apple-btn-danger-solid' : 'apple-btn-primary'} onClick={onConfirm}>{confirmLabel}</button>
        </div>
      </div>
    </div>
  );
}

/**
 * Promise-based confirmation:
 *   const [confirm, confirmDialog] = useConfirm();
 *   if (await confirm({ title: 'Delete user “guest”?', body: '…', confirmLabel: 'Delete user' })) { … }
 *   return <>{…}{confirmDialog}</>;
 */
export function useConfirm(): [(opts: ConfirmOptions) => Promise<boolean>, ReactNode] {
  const [opts, setOpts] = useState<ConfirmOptions | null>(null);
  const resolver = useRef<((ok: boolean) => void) | null>(null);

  const confirm = useCallback((o: ConfirmOptions) => new Promise<boolean>((resolve) => {
    resolver.current = resolve;
    setOpts(o);
  }), []);

  const settle = useCallback((ok: boolean) => {
    resolver.current?.(ok);
    resolver.current = null;
    setOpts(null);
  }, []);

  const onCancel = useCallback(() => settle(false), [settle]);
  const onConfirm = useCallback(() => settle(true), [settle]);

  const dialog = opts ? <ConfirmDialog open {...opts} onConfirm={onConfirm} onCancel={onCancel} /> : null;
  return [confirm, dialog];
}
