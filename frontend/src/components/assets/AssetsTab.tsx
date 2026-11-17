import { useCallback, useEffect, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { api, type AssetsOverview as Overview } from '../../api/client';
import AssetsOverview from './AssetsOverview';
import AssetDetail from './AssetDetail';
import AssetForm from './AssetForm';
import LoanDetail from './LoanDetail';
import { LoadingState, ErrorState } from '../ui';

// Query parameters that select a view inside the Assets tab, so the browser's
// back button and reloads work: ?asset=<id>, ?loan=<id>, ?add=1, ?edit=<id>.
export const ASSET_VIEW_PARAMS = ['asset', 'loan', 'add', 'edit'] as const;

export default function AssetsTab({ onNetWorthChanged, snapshotNetWorth }: {
  onNetWorthChanged?: () => void;
  /** Net worth shown in the page hero (latest snapshot). */
  snapshotNetWorth?: number;
}) {
  const [params, setParams] = useSearchParams();
  const [data, setData] = useState<Overview | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    setError(null);
    return api.listAssets().then(setData).catch(e => setError(e.message || 'Failed to load assets'));
  }, []);
  useEffect(() => { load(); }, [load]);

  const go = (next: Record<string, string> | null) => {
    const p = new URLSearchParams(params);
    ASSET_VIEW_PARAMS.forEach(k => p.delete(k));
    if (next) Object.entries(next).forEach(([k, v]) => p.set(k, v));
    setParams(p);
    window.scrollTo({ top: 0 });
  };

  // Snapshots are rebuilt in the background after each change; refetch the
  // page's net-worth figures once the backfill has had a moment.
  const changed = () => {
    load();
    if (onNetWorthChanged) setTimeout(onNetWorthChanged, 2500);
  };

  if (error && !data) {
    return <div className="mt-6"><ErrorState message="Your assets couldn’t be loaded." details={error} onRetry={load} /></div>;
  }
  if (!data) return <LoadingState message="Loading assets" />;

  const assetId = params.get('asset');
  const loanId = params.get('loan');
  const editId = params.get('edit');

  if (params.get('add')) {
    return <AssetForm asset={null} defaults={data.defaults} indexes={data.price_indexes} onCancel={() => go(null)} onSaved={a => { changed(); go({ asset: a.id }); }} />;
  }
  if (editId) {
    const asset = data.assets.find(a => a.id === editId);
    if (asset) {
      return <AssetForm asset={asset} defaults={data.defaults} indexes={data.price_indexes} onCancel={() => go({ asset: asset.id })} onSaved={a => { changed(); go({ asset: a.id }); }} />;
    }
  }
  if (loanId) {
    return (
      <LoanDetail
        key={loanId}
        loanId={loanId}
        onBack={() => go(null)}
        onOpenAsset={id => go({ asset: id })}
        onChanged={changed}
        onDeleted={() => { changed(); go(null); }}
      />
    );
  }
  if (assetId) {
    const asset = data.assets.find(a => a.id === assetId);
    if (asset) {
      return (
        <AssetDetail
          key={asset.id}
          asset={asset}
          loans={data.loans.filter(l => l.asset_id === asset.id)}
          onBack={() => go(null)}
          onEdit={() => go({ edit: asset.id })}
          onOpenLoan={id => go({ loan: id })}
          onChanged={changed}
          onDeleted={() => { changed(); go(null); }}
        />
      );
    }
  }
  return (
    <AssetsOverview
      data={data}
      snapshotNetWorth={snapshotNetWorth}
      onOpenAsset={id => go({ asset: id })}
      onOpenLoan={id => go({ loan: id })}
      onAdd={() => go({ add: '1' })}
    />
  );
}
