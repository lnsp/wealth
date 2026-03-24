import type { Account } from '../api/client';

interface Props {
  account: Account;
}

const institutionLabels: Record<string, string> = {
  sparkasse: 'Sparkasse',
  n26: 'N26',
  scalable_capital: 'Scalable Capital',
};

const typeLabels: Record<string, string> = {
  checking: 'Checking',
  savings: 'Savings',
  brokerage: 'Brokerage',
};

export default function AccountCard({ account }: Props) {
  const balance = account.balance ?? 0;
  const formatted = new Intl.NumberFormat('de-DE', {
    style: 'currency',
    currency: account.currency,
  }).format(balance);

  return (
    <div className="apple-card p-4 transition-shadow duration-200 hover:shadow-apple">
      <div className="flex items-start justify-between mb-3">
        <div>
          <h3 className="text-apple-subhead font-semibold text-gray-900">{account.name}</h3>
          <p className="text-apple-caption1 text-apple-gray-1 mt-0.5">
            {typeLabels[account.type] || account.type}
            {account.iban ? ` · ${account.iban}` : ''}
          </p>
        </div>
        <span className="apple-badge bg-apple-gray-6 text-apple-gray-1">
          {institutionLabels[account.institution] || account.institution}
        </span>
      </div>
      <p className={`text-apple-title3 ${balance >= 0 ? 'text-gray-900' : 'text-apple-red'}`}>
        {formatted}
      </p>
    </div>
  );
}
