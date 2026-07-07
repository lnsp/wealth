import type { Account } from '../api/client';
import ReactECharts from 'echarts-for-react';
import { useThemeColors } from '../hooks/useThemeColors';

interface Props {
  account: Account;
  sparklineData?: number[];
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

const typeLabels: Record<string, string> = {
  checking: 'Checking',
  savings: 'Savings',
  brokerage: 'Brokerage',
};

export default function AccountCard({ account, sparklineData, institutionCashTotal }: Props) {
  const tc = useThemeColors();
  const balance = account.balance ?? 0;
  const holdingsValue = account.holdings_value ?? 0;
  const cashBalance = account.cash_balance ?? 0;
  // Every brokerage gets the split — even with zero holdings (fully cashed
  // out) or zero cash (Morgan Stanley by design). The split makes it
  // explicit that the headline number is securities + idle cash combined.
  const isBrokerage = account.type === 'brokerage';

  // /api/accounts returns balance / cash_balance / holdings_value already
  // EUR-converted (HandleAccounts runs convertToEUR per account). Formatting
  // with `account.currency` mislabels a USD-denominated brokerage as USD when
  // the number on screen is actually EUR — pin to EUR to match the contract.
  const fmt = (n: number) =>
    new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' }).format(n);

  const hasSparkline = sparklineData && sparklineData.length > 2;

  // Determine sparkline color based on trend
  const sparkColor = hasSparkline
    ? sparklineData[sparklineData.length - 1] >= sparklineData[0]
      ? tc.sage
      : tc.claret
    : tc.forest;

  return (
    <div className="border-t border-divider pt-4 p-4 transition-colors hover:bg-parchment-deep">
      <div className="flex items-start justify-between mb-1">
        <div>
          <h3 className="text-[15px] font-semibold text-ink">{account.name}</h3>
          <p className="text-[12px] text-ink-muted mt-0.5">
            {typeLabels[account.type] || account.type}
            {account.iban ? ` · ${account.iban}` : ''}
          </p>
        </div>
        <span className="apple-badge bg-parchment-deep text-ink-muted">
          {institutionLabels[account.institution] || account.institution}
        </span>
      </div>

      <div className="flex items-end justify-between gap-3">
        <div className="shrink-0">
          <p className={`text-[20px] ${balance >= 0 ? 'text-ink' : 'text-claret'}`}>
            {fmt(balance)}
          </p>
          {isBrokerage && (
            <div className="flex gap-3 mt-1 text-[12px] text-ink-muted tabular-nums">
              <span>Holdings {fmt(holdingsValue)}</span>
              <span>Cash {fmt(cashBalance)}</span>
            </div>
          )}
          {balance < 0 && !isBrokerage && (
            <p className="text-[11px] text-amber mt-1">
              Negative balance — import may be missing earlier transactions
            </p>
          )}
        </div>

        {hasSparkline && (
          <div className="flex-1 max-w-[120px] h-[40px]">
            <ReactECharts
              option={{
                grid: { left: 0, right: 0, top: 0, bottom: 0 },
                xAxis: { type: 'category', show: false, data: sparklineData.map((_, i) => i) },
                yAxis: { type: 'value', show: false, min: Math.min(...sparklineData) * 0.95 },
                series: [{
                  type: 'line',
                  data: sparklineData,
                  smooth: 0.4,
                  showSymbol: false,
                  lineStyle: { width: 1.5, color: sparkColor },
                  areaStyle: { opacity: 0.1, color: sparkColor },
                }],
                animationDuration: 400,
              }}
              style={{ height: '40px', width: '100%' }}
              notMerge
            />
          </div>
        )}
      </div>

      {/* Einlagensicherung indicator for cash accounts.
          Spec thresholds: <90k green, 90k–100k amber, >100k red. The
          €100k cap is the EU statutory deposit-protection limit per
          institution per depositor; the 90k amber band gives a 10k buffer
          so users react before they're uncovered. */}
      {(account.type === 'checking' || account.type === 'savings') && institutionCashTotal != null && institutionCashTotal > 0 && (() => {
        const limit = 100_000;
        const amberFloor = 90_000;
        const scheme = account.institution === 'sparkasse' ? 'Institutssicherung' : 'Einlagensicherung';
        const status: 'ok' | 'near' | 'over' =
          institutionCashTotal > limit ? 'over' :
          institutionCashTotal >= amberFloor ? 'near' : 'ok';
        const border = status === 'over' ? 'border-claret' : status === 'near' ? 'border-amber' : 'border-sage';
        const dot = status === 'over' ? 'bg-claret' : status === 'near' ? 'bg-amber' : 'bg-sage';
        const fmtCur = (n: number) => new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR', maximumFractionDigits: 0 }).format(n);

        return (
          <div className={`mt-2 flex items-center gap-2 rounded-r-lg bg-inset px-2.5 py-1.5 text-[11px] text-ink-body border-l-[3px] ${border}`}>
            <span aria-hidden="true" className={`w-2 h-2 rounded-full shrink-0 ${dot}`} />
            <span>
              {scheme}: {fmtCur(institutionCashTotal)} / {fmtCur(limit)}
              {status === 'over' && ' — exceeds coverage limit'}
              {status === 'near' && ' — approaching limit'}
            </span>
          </div>
        );
      })()}
    </div>
  );
}
