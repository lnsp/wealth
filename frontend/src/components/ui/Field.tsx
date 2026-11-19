import { useEffect, useId, useRef, useState } from 'react';
import type { InputHTMLAttributes, ReactNode, SelectHTMLAttributes } from 'react';

interface FieldShellProps {
  label: string;
  htmlFor: string;
  hint?: ReactNode;
  error?: string;
  className?: string;
  children: ReactNode;
}

function FieldShell({ label, htmlFor, hint, error, className = '', children }: FieldShellProps) {
  return (
    <div className={`flex flex-col gap-1.5 min-w-0 ${className}`}>
      <label htmlFor={htmlFor} className="text-[13px] font-medium text-ink">{label}</label>
      {children}
      {error && <span id={`${htmlFor}-err`} className="text-[13px] text-claret">{error}</span>}
      {!error && hint && <span id={`${htmlFor}-hint`} className="text-[12px] text-ink-muted">{hint}</span>}
    </div>
  );
}

const BOX = 'relative flex items-center min-h-[44px] rounded-[6px] border bg-parchment-deep transition-colors focus-within:border-forest focus-within:outline focus-within:outline-2 focus-within:outline-offset-1 focus-within:outline-forest';
const CONTROL = 'flex-1 min-w-0 w-full h-[42px] bg-transparent border-0 outline-none px-3 text-[16px] text-ink';

export interface FieldProps extends Omit<InputHTMLAttributes<HTMLInputElement>, 'className'> {
  label: string;
  /** Unit shown inside the field on the right: "€ / year", "%", "years". */
  unit?: string;
  hint?: ReactNode;
  error?: string;
  className?: string;
}

/** Labelled text input, 44px tall, 16px text (no iOS zoom), optional unit suffix. */
export function Field({ label, unit, hint, error, className, id, ...input }: FieldProps) {
  const auto = useId();
  const fid = id ?? auto;
  return (
    <FieldShell label={label} htmlFor={fid} hint={hint} error={error} className={className}>
      <div className={`${BOX} ${error ? 'border-claret' : 'border-field'}`}>
        <input
          id={fid}
          {...input}
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? `${fid}-err` : hint ? `${fid}-hint` : undefined}
          className={CONTROL}
        />
        {unit && <span className="pr-3 text-[13px] text-ink-muted whitespace-nowrap">{unit}</span>}
      </div>
    </FieldShell>
  );
}

export interface NumberFieldProps extends Omit<FieldProps, 'value' | 'onChange' | 'type' | 'inputMode'> {
  value: number;
  onValueChange: (value: number) => void;
  /** Decimals shown when not focused. Default: as typed / minimal. */
  decimals?: number;
  integer?: boolean;
}

function parseLocaleNumber(text: string): number | null {
  const t = text.trim().replace(/\s/g, '');
  if (t === '' || t === '-' || t === '−') return null;
  // German input: "." groups thousands, "," is the decimal mark. Also accept a lone "." as decimal.
  let norm = t.replace('−', '-');
  if (norm.includes(',')) norm = norm.replace(/\./g, '').replace(',', '.');
  else if ((norm.match(/\./g) || []).length > 1 || /\.\d{3}$/.test(norm)) norm = norm.replace(/\./g, '');
  const n = Number(norm);
  return Number.isFinite(n) ? n : null;
}

function formatForEdit(n: number, decimals?: number): string {
  if (!Number.isFinite(n)) return '';
  return n.toLocaleString('de-DE', decimals === undefined
    ? { maximumFractionDigits: 6 }
    : { minimumFractionDigits: decimals, maximumFractionDigits: decimals });
}

/**
 * Numeric field that can be cleared and retyped: it keeps the user's draft text while
 * focused and only reports parseable numbers. An empty field on blur restores the last value.
 */
export function NumberField({ value, onValueChange, decimals, integer, onBlur, onFocus, ...rest }: NumberFieldProps) {
  const [draft, setDraft] = useState<string | null>(null);
  const focused = useRef(false);
  // Only an outside change replaces the text; while typing, reformatting "1100" as "1.100"
  // would move the cursor and turn the next digit into a decimal ("1.1000" = 1,1).
  useEffect(() => { if (!focused.current) setDraft(null); }, [value]);
  return (
    <Field
      {...rest}
      inputMode={integer ? 'numeric' : 'decimal'}
      value={draft ?? formatForEdit(value, decimals)}
      onFocus={(e) => { focused.current = true; setDraft(formatForEdit(value, decimals)); onFocus?.(e); }}
      onChange={(e) => {
        setDraft(e.target.value);
        const n = parseLocaleNumber(e.target.value);
        if (n !== null) onValueChange(integer ? Math.round(n) : n);
      }}
      onBlur={(e) => { focused.current = false; setDraft(null); onBlur?.(e); }}
    />
  );
}

export interface SelectFieldProps extends Omit<SelectHTMLAttributes<HTMLSelectElement>, 'className'> {
  label: string;
  hint?: ReactNode;
  error?: string;
  className?: string;
  /** Hide the visible label (still announced). For dense rows where a column header names the field. */
  labelHidden?: boolean;
}

/** Labelled native select with a visible chevron. */
export function SelectField({ label, hint, error, className, id, labelHidden, children, ...select }: SelectFieldProps) {
  const auto = useId();
  const fid = id ?? auto;
  const control = (
    <div className={`${BOX} ${error ? 'border-claret' : 'border-field'}`}>
      <select
        id={fid}
        {...select}
        aria-label={labelHidden ? label : undefined}
        aria-invalid={error ? true : undefined}
        className={`${CONTROL} appearance-none pr-9 cursor-pointer`}
      >
        {children}
      </select>
      <svg className="pointer-events-none absolute right-3 w-4 h-4 text-ink-muted" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.5} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M6 9l6 6 6-6" />
      </svg>
    </div>
  );
  if (labelHidden) return <div className={`min-w-0 ${className ?? ''}`}>{control}</div>;
  return (
    <FieldShell label={label} htmlFor={fid} hint={hint} error={error} className={className}>
      {control}
    </FieldShell>
  );
}
