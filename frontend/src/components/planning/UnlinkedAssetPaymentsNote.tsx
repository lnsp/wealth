import { Link } from 'react-router-dom';
import type { UnlinkedAssetPayment } from '../../api/client';
import { fmtDate, fmtEUR } from '../../utils/format';

/**
 * Points at withdrawals that probably paid for an asset but aren't linked to
 * it. Unlinked, they count as money leaving for good and lower the trailing
 * contribution; linked, they are a swap from cash into the asset and drop out.
 */
export default function UnlinkedAssetPaymentsNote({ payments }: { payments?: UnlinkedAssetPayment[] }) {
  if (!payments || payments.length === 0) return null;
  return (
    <span className="mt-2 block text-[13px] text-ink-body">
      {payments.map((p, i) => (
        <span key={p.transaction_id}>
          {i > 0 && ' '}
          The withdrawal of <span data-privacy="blur">{fmtEUR(p.amount)}</span> on {fmtDate(p.date)} looks like the payment for {p.asset} and
          is counted as money leaving your accounts, which lowers this rate.{' '}
          <Link to={`/?edit=${encodeURIComponent(p.asset_id)}`} className="font-semibold text-forest underline underline-offset-2">Link it to {p.asset}</Link>{' '}
          to treat it as a one-off move into the asset.
        </span>
      ))}
    </span>
  );
}
