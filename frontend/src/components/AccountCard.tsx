import type { Account } from '../api/client';
import EChartWrapper from './charts/EChartWrapper';
import { Chip } from './ui';
import { useThemeColors } from '../hooks/useThemeColors';
import { fmtEUR, labelFor } from '../utils/format';

interface Props {
  account: Account;
  sparklineData?: number[];
  /** What the sparkline shows, e.g. "All cash, 90 days" — it is not this account alone. */
  sparklineLabel?: string;
  institutionCashTotal?: number;
}

const institutionLabels: Record<string, string> = {
  sparkasse: 'Sparkasse',
  n26: 'N26',
  scalable_capital: 'Scalable Capital',
  morgan_stanley: 'Morgan Stanley',
  revolut: 'Revolut',
  ing: 'ING',
  delta: 'Delta',
};

/** Display name for an institution identifier. */
export function institutionLabel(institution: string): string {
  return institutionLabels[institution] || labelFor(institution);
}

/** Statutory deposit insurance per depositor and bank (EU: Einlagensicherung). */
export const DEPOSIT_INSURANCE_LIMIT = 100_000;
/** "Near limit" from here, to leave room to react before the limit. */
export const DEPOSIT_INSURANCE_NEAR = 90_000;
/** US brokers: cash is protected by SIPC, not by EU deposit insurance. */
const SIPC_INSTITUTIONS = new Set(['morgan_stanley']);

export interface DepositProtection {
  /** Scheme name for the label: "Einlagensicherung", "Institutssicherung" or "SIPC". */
  scheme: string;
  /** EUR limit the status is judged against; null when it is not a EUR limit (SIPC). */
  limit: number | null;
  status: 'ok' | 'near' | 'over' | 'other';
}

/**
 * How a bank's or broker's cash (in EUR, all accounts there together) is
 * protected. One rule for the whole app: covered below 90k, near the limit
 * from 90k, over the limit above 100k. Savings banks are covered by their
 * Institutssicherung; US brokers by SIPC (US$ 250,000 for cash), which is
 * not EU deposit insurance and is not judged against the EUR limit.
 */
export function depositProtection(institution: string, cashEUR: number): DepositProtection {
  if (SIPC_INSTITUTIONS.has(institution)) return { scheme: 'SIPC', limit: null, status: 'other' };
  const scheme = institution === 'sparkasse' ? 'Institutssicherung' : 'Einlagensicherung';
  const status = cashEUR > DEPOSIT_INSURANCE_LIMIT ? 'over' : cashEUR >= DEPOSIT_INSURANCE_NEAR ? 'near' : 'ok';
  return { scheme, limit: DEPOSIT_INSURANCE_LIMIT, status };
}

export default function AccountCard({ account, sparklineData, sparklineLabel, institutionCashTotal }: Props) {
  const tc = useThemeColors();
  // /api/accounts returns balance / cash_balance / holdings_value already
  // EUR-converted, so everything here is formatted as EUR regardless of
  // `account.currency`.
  const balance = account.balance ?? 0;
  const holdingsValue = account.holdings_value ?? 0;
  const cashBalance = account.cash_balance ?? 0;
  // Every brokerage gets the split — even with zero holdings (fully cashed
  // out) or zero cash. The split makes it explicit that the headline number
  // is securities + idle cash combined.
  const isBrokerage = account.type === 'brokerage';

  // A flat series has nothing to show: omit the sparkline rather than draw an
  // empty box.
  const min = sparklineData && sparklineData.length ? Math.min(...sparklineData) : 0;
  const max = sparklineData && sparklineData.length ? Math.max(...sparklineData) : 0;
  const hasSparkline = !!sparklineData && sparklineData.length > 2 && max - min > Math.max(1, Math.abs(max) * 0.0005);
  const pad = (max - min) * 0.15;

  return (
    <div className="border-t border-divider pt-4 pb-1 min-w-0">
      <div className="flex items-start justify-between gap-3 mb-1.5">
        <div className="min-w-0">
          <h4 className="text-[15px] font-semibold text-ink break-words">{account.name}</h4>
          <p className="text-[12px] text-ink-muted mt-0.5 break-words">
            {labelFor(account.type)}
            {account.iban ? <> · <span data-privacy="blur">{account.iban}</span></> : ''}
          </p>
        </div>
        <Chip className="shrink-0">{institutionLabel(account.institution)}</Chip>
      </div>

      <div className="flex items-end justify-between gap-3">
        <div className="min-w-0">
          <p className={`font-serif text-[24px] leading-8 ${balance < 0 ? 'text-claret' : 'text-ink'}`} data-privacy="blur">
            {fmtEUR(balance)}
          </p>
          {isBrokerage && (
            <p className="flex flex-wrap gap-x-3 text-[12px] text-ink-muted">
              <span>Holdings <span data-privacy="blur">{fmtEUR(holdingsValue)}</span></span>
              <span>Cash <span data-privacy="blur">{fmtEUR(cashBalance)}</span></span>
            </p>
          )}
          {balance < 0 && !isBrokerage && (
            <p className="text-[12px] text-ink-body mt-1">
              <Chip tone="warning" className="mr-1.5">Check</Chip>
              Negative balance — the import may be missing earlier transactions.
            </p>
          )}
        </div>

        {hasSparkline && (
          <div className="flex-1 max-w-[120px] min-w-[72px]">
            <div role="img" aria-label={`${sparklineLabel ?? 'Trend'}: ${sparklineData[sparklineData.length - 1] >= sparklineData[0] ? 'up' : 'down'} over the period`}>
              <EChartWrapper
                option={{
                  grid: { left: 2, right: 2, top: 4, bottom: 4 },
                  tooltip: { show: false },
                  xAxis: { type: 'category', show: false, data: sparklineData.map((_, i) => i), boundaryGap: false },
                  yAxis: { type: 'value', show: false, min: min - pad, max: max + pad },
                  series: [{
                    type: 'line',
                    data: sparklineData,
                    showSymbol: false,
                    silent: true,
                    color: tc.forest,
                    lineStyle: { width: 1.5 },
                  }],
                }}
                height="36px"
              />
            </div>
            {sparklineLabel && <p className="text-[11px] text-ink-muted text-right mt-0.5">{sparklineLabel}</p>}
          </div>
        )}
      </div>

      {/* Deposit protection for cash accounts, by the shared rule in depositProtection. */}
      {(account.type === 'checking' || account.type === 'savings') && institutionCashTotal != null && institutionCashTotal > 0 && (() => {
        const p = depositProtection(account.institution, institutionCashTotal);
        if (p.limit === null) {
          return (
            <p className="mt-2.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-[12px] text-ink-muted">
              <Chip>{p.scheme}</Chip>
              <span>Cash is protected by {p.scheme} (US), not by EU deposit insurance</span>
            </p>
          );
        }
        return (
          <p className="mt-2.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-[12px] text-ink-muted">
            <Chip tone={p.status === 'over' ? 'danger' : p.status === 'near' ? 'warning' : 'success'}>
              {p.status === 'over' ? 'Over limit' : p.status === 'near' ? 'Near limit' : 'Covered'}
            </Chip>
            <span>
              Deposit insurance ({p.scheme}): <span data-privacy="blur">{fmtEUR(institutionCashTotal)}</span> of {fmtEUR(p.limit)} at this institution
            </span>
          </p>
        );
      })()}
    </div>
  );
}
