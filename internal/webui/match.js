export function isModeDisabled(total, mode) {
  if (!total) return false;
  if (mode === 'matching') return total.matching === 0 && total.pending > 0;
  if (mode === 'pending') return total.pending === 0 && total.matching > 0;
  return false;
}

export function getFallbackMode(total, mode, isEmpty) {
  if (!total || !isEmpty) return null;
  if (mode === 'matching' && total.matching === 0 && total.pending > 0) return 'pending';
  if (mode === 'pending' && total.pending === 0 && total.matching > 0) return 'matching';
  return null;
}
