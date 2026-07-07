interface MetricCardProps {
  label: string;
  value: string;
  sublabel?: string;
  className?: string;
  valueClassName?: string;
}

export default function MetricCard({ label, value, sublabel, className = '', valueClassName = 'text-ink' }: MetricCardProps) {
  return (
    <div className={`rounded-xl bg-parchment-deep p-3 ${className}`}>
      <p className="text-[12px] text-ink-muted mb-1">{label}</p>
      <p className={`font-serif text-[20px] font-semibold tabular-nums ${valueClassName}`}>{value}</p>
      {sublabel && <p className="text-[11px] text-ink-muted mt-0.5">{sublabel}</p>}
    </div>
  );
}
