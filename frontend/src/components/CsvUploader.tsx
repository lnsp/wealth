import { useState, useCallback, useRef } from 'react';
import { api, type ImportResult, type Account } from '../api/client';
import { SelectField, ErrorState } from './ui';
import { labelFor } from '../utils/format';

interface Props {
  accounts: Account[];
  onImportComplete: (result: ImportResult) => void;
}

export default function CsvUploader({ accounts, onImportComplete }: Props) {
  const [dragOver, setDragOver] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [selectedAccount, setSelectedAccount] = useState<string>('');
  const [accountError, setAccountError] = useState<string | undefined>(undefined);
  const [error, setError] = useState<string | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const handleFile = useCallback(async (file: File) => {
    if (!selectedAccount) {
      setAccountError('Select the account this file belongs to first.');
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
    // Reset so choosing the same file again fires a change event.
    e.target.value = '';
    if (file) handleFile(file);
  }, [handleFile]);

  return (
    <div className="space-y-4 max-w-xl">
      <SelectField
        label="Account"
        value={selectedAccount}
        error={accountError}
        onChange={(e) => { setSelectedAccount(e.target.value); setAccountError(undefined); }}
      >
        <option value="">Select account...</option>
        {accounts.map((acc) => (
          <option key={acc.id} value={acc.id}>
            {acc.name} ({labelFor(acc.institution)})
          </option>
        ))}
      </SelectField>

      {/* One real button for keyboard, pointer and drag & drop; the file input stays out of the tab order. */}
      <input
        ref={inputRef}
        type="file"
        accept=".csv,text/csv"
        onChange={handleChange}
        className="hidden"
        tabIndex={-1}
        aria-hidden="true"
      />
      <button
        type="button"
        disabled={uploading}
        aria-busy={uploading}
        onClick={() => inputRef.current?.click()}
        onDragOver={(e) => { e.preventDefault(); setDragOver(true); }}
        onDragLeave={() => setDragOver(false)}
        onDrop={handleDrop}
        className={`w-full flex flex-col items-center justify-center gap-1 rounded-apple border border-dashed px-4 py-6 md:py-8 min-h-[44px] text-center text-ink-body transition-colors disabled:cursor-wait ${
          dragOver ? 'border-forest bg-inset' : 'border-ink-muted bg-transparent hover:border-forest'
        }`}
      >
        {uploading ? (
          <span role="status" className="text-[14px] font-medium text-ink">Importing…</span>
        ) : (
          <>
            <svg className="h-6 w-6 text-forest" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.5} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M12 16V5M8 9l4-4 4 4M5 19h14" />
            </svg>
            <span className="text-[14px] font-medium text-ink">Choose a CSV file</span>
            <span className="text-[13px]">or drop it here</span>
          </>
        )}
      </button>

      {error && (
        <ErrorState message={`The file could not be imported: ${error}`} />
      )}
    </div>
  );
}
