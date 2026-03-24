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
    <div className="rounded-lg border border-gray-200 bg-white p-4 shadow-sm">
      <div className="flex items-center justify-between mb-2">
        <h3 className="font-medium text-gray-900">{account.name}</h3>
        <span className="text-xs rounded-full bg-gray-100 px-2 py-1 text-gray-600">
          {institutionLabels[account.institution] || account.institution}
        </span>
      </div>
      <p className="text-xs text-gray-500 mb-3">
        {typeLabels[account.type] || account.type}
        {account.iban ? ` \u00b7 ${account.iban}` : ''}
      </p>
      <p className={`text-xl font-semibold ${balance >= 0 ? 'text-gray-900' : 'text-red-600'}`}>
        {formatted}
      </p>
    </div>
  );
}
