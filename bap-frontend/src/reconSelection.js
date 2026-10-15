import { useState } from 'react';

// Which contracts the next /reconcile to one network participant carries:
// the ticked ones among those due or disputed (selectable, from the server)
// that match the page's filters (visible). Any mix goes, as a new batch.
// Everything selectable starts ticked, and so does a contract that becomes
// selectable later.
//
// Shared with bpp-frontend/src/reconSelection.js: keep the two copies in step.
export function useReconSelection(rows, visible) {
  const [unticked, setUnticked] = useState(() => new Set());

  const shown = rows.filter(r => r.selectable && visible(r));
  const ticked = shown.filter(r => !unticked.has(r.contract_id));
  const all = shown.length > 0 && ticked.length === shown.length;

  const toggle = r => setUnticked(u => {
    const n = new Set(u);
    n.has(r.contract_id) ? n.delete(r.contract_id) : n.add(r.contract_id);
    return n;
  });
  const toggleAll = () => setUnticked(u => {
    const n = new Set(u);
    shown.forEach(r => (all ? n.add(r.contract_id) : n.delete(r.contract_id)));
    return n;
  });

  return {
    ticked,
    total: shown.length,
    isTicked: r => !unticked.has(r.contract_id),
    toggle,
    toggleAll,
    header: { checked: all, indeterminate: ticked.length > 0 && !all, disabled: shown.length === 0 },
  };
}

// inDateRange: is a row's contract created_at within [from, to] (YYYY-MM-DD,
// local dates; either may be empty)?
export function inDateRange(r, from, to) {
  if (!from && !to) return true;
  if (!r.created_at) return false;
  const d = new Date(r.created_at);
  const day = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
  return (!from || day >= from) && (!to || day <= to);
}
