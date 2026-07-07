import type { HoldingRow } from '../api/client';
import { useThemeColors } from '../hooks/useThemeColors';

interface Props {
  holdings: HoldingRow[];
  onSecurityClick?: (isin: string) => void;
}

export default function HoldingsTable({ holdings, onSecurityClick }: Props) {
  const tc = useThemeColors();

  function weightColor(pct: number): string {
    if (pct >= 30) return tc.claret;
    if (pct >= 20) return tc.gold;
    if (pct >= 10) return tc.forest;
    return tc.sage;
  }

  const ALLOC_COLORS = [tc.forest, tc.sage, tc.gold, tc.walnut, tc.claret, tc.slate, tc.gold, tc.claret];
  const fmt = (n: number) =>
    new Intl.NumberFormat('de-DE', { style: 'currency', currency: 'EUR' }).format(n);
  const fmtQty = (n: number) =>
    new Intl.NumberFormat('de-DE', { minimumFractionDigits: 3, maximumFractionDigits: 3 }).format(n);
  const fmtPct = (n: number) =>
    `${n >= 0 ? '+' : ''}${n.toFixed(2)}%`;

  const hasMarketPrices = holdings.some((h) => h.market_value != null);

  // Sort by weight descending
  const sorted = [...holdings].sort((a, b) => (b.weight_pct ?? 0) - (a.weight_pct ?? 0));

  if (sorted.length === 0) {
    return (
      <div className="px-5 py-12 text-center text-[16px] text-ink-muted">
        No holdings yet. Import transactions to see your portfolio.
      </div>
    );
  }

  return (
    <>
      {/* Stacked allocation bar */}
      {sorted.some(h => h.weight_pct != null) && (
        <div className="mb-4 px-1 md:px-0">
          <div className="flex h-3 w-full rounded-full overflow-hidden">
            {sorted.map((h, i) => (
              <div
                key={`${h.account_id}-${h.security_isin}`}
                className="transition-all duration-300"
                style={{
                  width: `${h.weight_pct ?? 0}%`,
                  backgroundColor: ALLOC_COLORS[i % ALLOC_COLORS.length],
                }}
                title={`${h.name}: ${(h.weight_pct ?? 0).toFixed(1)}%`}
              />
            ))}
          </div>
          <div className="flex flex-wrap gap-x-4 gap-y-1 mt-2">
            {sorted.map((h, i) => (
              <div key={`${h.account_id}-${h.security_isin}-legend`} className="flex items-center gap-1.5">
                <div className="w-2.5 h-2.5 rounded-sm shrink-0" style={{ backgroundColor: ALLOC_COLORS[i % ALLOC_COLORS.length] }} />
                <span className="text-[12px] text-ink-muted">
                  <span data-privacy="blur">{h.name.length > 20 ? h.name.slice(0, 18) + '...' : h.name}</span> <span data-privacy="blur" className="tabular-nums">{(h.weight_pct ?? 0).toFixed(1)}%</span>
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Mobile: card layout */}
      <div className="md:hidden space-y-3 -mx-1">
        {sorted.map((h, i) => {
          const costBasis = h.quantity * h.avg_cost_basis;
          const pl = h.unrealized_pl ?? 0;
          const plPct = costBasis !== 0 ? (pl / costBasis) * 100 : 0;
          const color = ALLOC_COLORS[i % ALLOC_COLORS.length];

          return (
            <div
              key={`${h.account_id}-${h.security_isin}`}
              className="rounded-apple bg-parchment-deep px-4 py-3"
            >
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0 flex-1">
                  <p className={`text-[15px] font-medium text-ink truncate ${onSecurityClick ? 'cursor-pointer hover:text-forest' : ''}`}
                    onClick={() => onSecurityClick?.(h.security_isin)}>{h.name}</p>
                  <p className="text-[12px] text-ink-muted mt-0.5">
                    {h.asset_class}{h.symbol ? ` · ${h.symbol}` : ''}
                  </p>
                </div>
                {hasMarketPrices && h.market_value != null ? (
                  <div className="text-right shrink-0">
                    <p className="text-[15px] font-medium tabular-nums">{fmt(h.market_value)}</p>
                    <p className={`text-[12px] tabular-nums ${pl >= 0 ? 'text-sage' : 'text-claret'}`}>
                      {pl >= 0 ? '+' : ''}{fmt(pl)} ({fmtPct(plPct)})
                    </p>
                  </div>
                ) : (
                  <p className="text-[15px] font-medium tabular-nums shrink-0">{fmt(costBasis)}</p>
                )}
              </div>
              {/* Weight bar */}
              {h.weight_pct != null && (
                <div className="mt-2 flex items-center gap-2">
                  <div className="flex-1 h-2 rounded-full bg-divider">
                    <div
                      className="h-2 rounded-full transition-all duration-300"
                      style={{ width: `${Math.min(h.weight_pct, 100)}%`, backgroundColor: color }}
                    />
                  </div>
                  <span className="text-[12px] font-medium tabular-nums shrink-0" style={{ color }}>
                    {h.weight_pct.toFixed(1)}%
                  </span>
                </div>
              )}
              <div className="flex items-center gap-4 mt-2 text-[12px] text-ink-muted tabular-nums">
                <span>{fmtQty(h.quantity)} units</span>
                <span>Avg {fmt(h.avg_cost_basis)}</span>
                {h.total_dividends > 0 && (
                  <span className="text-sage">Div {fmt(h.total_dividends)}</span>
                )}
              </div>
            </div>
          );
        })}
      </div>

      {/* Desktop: table layout */}
      <p className="hidden md:block text-[11px] text-ink-muted text-right mb-1 px-1">Scroll for more →</p>
      <div className="hidden md:block overflow-x-auto -mx-5">
        <table className="w-full min-w-[800px]">
          <thead>
            <tr className="text-left font-serif text-[11px] text-ink-muted uppercase tracking-[0.1em]">
              <th className="px-5 pb-2 font-medium">Name</th>
              <th className="px-5 pb-2 font-medium text-right">Quantity</th>
              <th className="px-5 pb-2 font-medium text-right">Avg Cost</th>
              {hasMarketPrices && <th className="px-5 pb-2 font-medium text-right">Price</th>}
              {hasMarketPrices && <th className="px-5 pb-2 font-medium text-right">Market Value</th>}
              {hasMarketPrices && <th className="px-5 pb-2 font-medium text-right">P&L</th>}
              {hasMarketPrices && <th className="px-5 pb-2 font-medium text-right">FX Impact</th>}
              <th className="px-5 pb-2 font-medium text-right">Weight</th>
              <th className="px-5 pb-2 font-medium text-right">Dividends</th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((h, i) => {
              const costBasis = h.quantity * h.avg_cost_basis;
              const pl = h.unrealized_pl ?? 0;
              const plPct = costBasis !== 0 ? (pl / costBasis) * 100 : 0;
              const color = ALLOC_COLORS[i % ALLOC_COLORS.length];

              return (
                <tr
                  key={`${h.account_id}-${h.security_isin}`}
                  className={`transition-colors hover:bg-parchment-deep ${
                    i < sorted.length - 1 ? 'border-b border-divider' : ''
                  }`}
                >
                  <td className="px-5 py-3">
                    <div className="flex items-center gap-2">
                      <div className="w-2.5 h-2.5 rounded-sm shrink-0" style={{ backgroundColor: color }} />
                      <div>
                        <div className={`text-[15px] font-medium text-ink ${onSecurityClick ? 'cursor-pointer hover:text-forest' : ''}`}
                          onClick={() => onSecurityClick?.(h.security_isin)}>{h.name}</div>
                        <div className="text-[12px] text-ink-muted mt-0.5">
                          {h.asset_class}{h.symbol ? ` · ${h.symbol}` : ''} · {h.security_isin}
                        </div>
                      </div>
                    </div>
                  </td>
                  <td className="px-5 py-3 text-right text-[15px] tabular-nums">{fmtQty(h.quantity)}</td>
                  <td className="px-5 py-3 text-right text-[15px] tabular-nums">{fmt(h.avg_cost_basis)}</td>
                  {hasMarketPrices && (
                    <td className="px-5 py-3 text-right text-[15px] tabular-nums">
                      {h.current_price != null ? fmt(h.current_price) : '—'}
                    </td>
                  )}
                  {hasMarketPrices && (
                    <td className="px-5 py-3 text-right text-[15px] tabular-nums font-medium">
                      {h.market_value != null ? fmt(h.market_value) : fmt(costBasis)}
                    </td>
                  )}
                  {hasMarketPrices && (
                    <td className="px-5 py-3 text-right text-[15px] tabular-nums">
                      {h.unrealized_pl != null ? (
                        <div className={pl >= 0 ? 'text-sage' : 'text-claret'}>
                          <div>{pl >= 0 ? '+' : ''}{fmt(pl)}</div>
                          <div className="text-[12px]">{fmtPct(plPct)}</div>
                        </div>
                      ) : '—'}
                    </td>
                  )}
                  {hasMarketPrices && (
                    <td className="px-5 py-3 text-right text-[15px] tabular-nums">
                      {h.fx_impact_pct != null ? (
                        <div>
                          <div className={h.fx_impact_pct >= 0 ? 'text-sage' : 'text-claret'}>
                            {h.fx_impact_pct >= 0 ? '+' : ''}{h.fx_impact_pct.toFixed(1)}%
                          </div>
                          {h.fx_exposure && <div className="text-[12px] text-ink-muted">{h.fx_exposure}</div>}
                        </div>
                      ) : <span className="text-ink-muted/40">—</span>}
                    </td>
                  )}
                  <td className="px-5 py-3 text-right w-28">
                    {h.weight_pct != null ? (
                      <div>
                        <span className="text-[15px] font-semibold tabular-nums" style={{ color: weightColor(h.weight_pct) }}>
                          {h.weight_pct.toFixed(1)}%
                        </span>
                        <div className="mt-1 h-1.5 w-full rounded-full bg-divider">
                          <div
                            className="h-1.5 rounded-full transition-all duration-300"
                            style={{ width: `${Math.min(h.weight_pct, 100)}%`, backgroundColor: color }}
                          />
                        </div>
                      </div>
                    ) : '—'}
                  </td>
                  <td className="px-5 py-3 text-right text-[15px] tabular-nums text-sage">{fmt(h.total_dividends)}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </>
  );
}
