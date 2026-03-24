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
        className="w-full rounded-lg border border-gray-300 px-4 py-2 text-sm focus:border-blue-500 focus:outline-none"
      >
        <option value="">Select account...</option>
        {accounts.map((acc) => (
          <option key={acc.id} value={acc.id}>
            {acc.name} ({acc.institution})
          </option>
        ))}
      </select>

      <div
        onDragOver={(e) => { e.preventDefault(); setDragOver(true); }}
        onDragLeave={() => setDragOver(false)}
        onDrop={handleDrop}
        className={`rounded-lg border-2 border-dashed p-8 text-center transition-colors ${
          dragOver ? 'border-blue-500 bg-blue-50' : 'border-gray-300 hover:border-gray-400'
        }`}
      >
        {uploading ? (
          <p className="text-gray-500">Importing...</p>
        ) : (
          <>
            <p className="text-gray-600 mb-2">Drag & drop a CSV file here</p>
            <p className="text-gray-400 text-sm mb-4">or</p>
            <label className="cursor-pointer rounded-lg bg-blue-600 px-4 py-2 text-sm text-white hover:bg-blue-700">
              Browse files
              <input type="file" accept=".csv" onChange={handleChange} className="hidden" />
            </label>
          </>
        )}
      </div>

      {error && (
        <div className="rounded-lg bg-red-50 border border-red-200 p-3 text-sm text-red-700">
          {error}
        </div>
      )}
    </div>
  );
}
