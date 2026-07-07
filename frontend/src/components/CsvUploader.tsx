import { useState, useCallback } from 'react';
import { api, type ImportResult, type Account } from '../api/client';

interface Props {
  accounts: Account[];
  onImportComplete: (result: ImportResult) => void;
}

export default function CsvUploader({ accounts, onImportComplete }: Props) {
  const [dragOver, setDragOver] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [selectedAccount, setSelectedAccount] = useState<string>('');
  const [error, setError] = useState<string | null>(null);

  const handleFile = useCallback(async (file: File) => {
    if (!selectedAccount) {
      setError('Please select an account first');
      return;
    }
    setUploading(true);
    setError(null);
    try {
      const result = await api.importCSV(file, selectedAccount);
      onImportComplete(result);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Import failed');
    } finally {
      setUploading(false);
    }
  }, [selectedAccount, onImportComplete]);

  const handleDrop = useCallback((e: React.DragEvent) => {
    e.preventDefault();
    setDragOver(false);
    const file = e.dataTransfer.files[0];
    if (file) handleFile(file);
  }, [handleFile]);

  const handleChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file) handleFile(file);
  }, [handleFile]);

  return (
    <div className="space-y-4">
      <select
        value={selectedAccount}
        onChange={(e) => setSelectedAccount(e.target.value)}
        className="apple-select"
      >
        <option value="">Select account...</option>
        {accounts.map((acc) => (
          <option key={acc.id} value={acc.id}>
            {acc.name} ({acc.institution})
          </option>
        ))}
      </select>

      {/* Desktop: drag & drop zone */}
      <div
        onDragOver={(e) => { e.preventDefault(); setDragOver(true); }}
        onDragLeave={() => setDragOver(false)}
        onDrop={handleDrop}
        className={`hidden md:block rounded-[16px] border-2 border-dashed p-10 text-center transition-all duration-200 ${
          dragOver
            ? 'border-forest bg-inset border-l-[3px] border-forest'
            : 'border-divider hover:border-ink-muted'
        }`}
      >
        {uploading ? (
          <p className="text-[16px] text-ink-muted">Importing...</p>
        ) : (
          <>
            <div className="mx-auto mb-3 flex h-10 w-10 items-center justify-center rounded-full bg-parchment-deep">
              <svg className="h-5 w-5 text-ink-muted" fill="none" viewBox="0 0 24 24" strokeWidth={1.5} stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" d="M3 16.5v2.25A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75V16.5m-13.5-9L12 3m0 0l4.5 4.5M12 3v13.5" />
              </svg>
            </div>
            <p className="text-[16px] text-ink-body mb-1">Drag & drop a CSV file here</p>
            <p className="text-[12px] text-ink-muted mb-4">or</p>
            <label className="apple-btn-primary cursor-pointer">
              Choose File
              <input type="file" accept=".csv" onChange={handleChange} className="hidden" />
            </label>
          </>
        )}
      </div>

      {/* Mobile: simple upload button */}
      <div className="md:hidden">
        {uploading ? (
          <p className="text-[16px] text-ink-muted text-center py-4">Importing...</p>
        ) : (
          <label className="apple-btn-primary w-full flex items-center justify-center gap-2 cursor-pointer">
            <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" strokeWidth={2} stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" d="M3 16.5v2.25A2.25 2.25 0 005.25 21h13.5A2.25 2.25 0 0021 18.75V16.5m-13.5-9L12 3m0 0l4.5 4.5M12 3v13.5" />
            </svg>
            Upload CSV File
            <input type="file" accept=".csv" onChange={handleChange} className="hidden" />
          </label>
        )}
      </div>

      {error && (
        <div className="rounded-[12px] bg-inset border-l-[3px] border-claret px-4 py-3 text-[15px] text-claret">
          {error}
        </div>
      )}
    </div>
  );
}
