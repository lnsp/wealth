import Analysis from './Analysis';

// Tax page — renders Analysis with tax tab pre-selected
// TODO: Extract tax sections into standalone components in future refactor
export default function Tax() {
  return <Analysis defaultTab="tax" />;
}
