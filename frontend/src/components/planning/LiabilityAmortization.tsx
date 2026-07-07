import { usePlanning } from './PlanningContext';

// Mortgage / loan amortization table. Assumes 1.8% annual rate and 25y
// term for all liabilities — these are reasonable defaults for a German
// Bauspar/Annuitätendarlehen but should become per-account inputs in a
// later iteration (rate + remaining term live on `accounts` already).
export default function LiabilityAmortization() {
  const { accounts, fmt } = usePlanning();
  const liabilities = accounts.filter(a => a.type === 'liability' && (a.balance ?? 0) < 0);
  if (liabilities.length === 0) return null;

  return (
    <div className="border-t border-divider pt-6 py-3 md:py-5">
      <h2 className="font-serif text-heading text-ink mb-3 md:mb-4 px-1 md:px-0">Liability Amortization</h2>
      <p className="text-[13px] text-ink-muted mb-3">
        Monthly payment schedule for your liabilities.
      </p>
      {liabilities.map(acc => {
        const principal = Math.abs(acc.balance ?? 0);
        const annualRate = 0.018;
        const termYears = 25;
        const monthlyRate = annualRate / 12;
        const numPayments = termYears * 12;
        const monthlyPayment = monthlyRate > 0
          ? principal * (monthlyRate * Math.pow(1 + monthlyRate, numPayments)) / (Math.pow(1 + monthlyRate, numPayments) - 1)
          : principal / numPayments;
        const totalPayment = monthlyPayment * numPayments;
        const totalInterest = totalPayment - principal;

        return (
          <div key={acc.id} className="mb-4">
            <p className="text-[15px] font-medium text-ink mb-2">{acc.name}</p>
            <div className="grid grid-cols-2 md:grid-cols-4 gap-2">
              <div className="rounded-lg bg-parchment-deep p-2.5">
                <p className="text-[11px] text-ink-muted">Outstanding</p>
                <p className="text-[13px] font-semibold tabular-nums">{fmt(principal)}</p>
              </div>
              <div className="rounded-lg bg-parchment-deep p-2.5">
                <p className="text-[11px] text-ink-muted">Monthly Payment</p>
                <p className="text-[13px] font-semibold tabular-nums">{fmt(monthlyPayment)}</p>
              </div>
              <div className="rounded-lg bg-parchment-deep p-2.5">
                <p className="text-[11px] text-ink-muted">Total Interest</p>
                <p className="text-[13px] font-semibold tabular-nums text-claret">{fmt(totalInterest)}</p>
              </div>
              <div className="rounded-lg bg-parchment-deep p-2.5">
                <p className="text-[11px] text-ink-muted">Payoff</p>
                <p className="text-[13px] font-semibold tabular-nums">{termYears} years</p>
              </div>
            </div>
          </div>
        );
      })}
    </div>
  );
}
