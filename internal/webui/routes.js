// Hash routing for the WebUI. Every navigation a user can perform maps to a
// URL under the `#/` prefix so the browser back/forward buttons reproduce the
// exact view and a refresh deep-links back into it. The hash is the single
// source of truth for what the detail panel shows: app.js parses it on
// hashchange (applyRoute) and every navigation handler only navigates - it
// never mutates the view directly.
//
// Routes:
//   #/entry/{id}[/{tab}]                             normal detail (request|response|origin)
//   #/replay/{run}/{seq}[/{tab}]                     replay event detail (+ match)
//   #/replay/{run}/{seq}/match/{scope}[/{entryId}]   match scope (matching|all) + selected candidate
//   #/replay/{run}/{seq}/entry/{entryId}/{n}[/{tab}] recorded entry opened from a replay event

const TABS = ['request', 'response', 'origin', 'match'];

export function parseRoute(hash) {
  const h = (hash || '').replace(/^#\/?/, '');
  if (!h) return null;
  const parts = h.split('/').map(part => decodeURIComponent(part));
  const head = parts[0];
  if (head === 'entry') {
    const id = parts[1];
    if (!id) return null;
    return { kind: 'entry', id, tab: parts[2] || 'request' };
  }
  if (head === 'replay') {
    const run = parts[1];
    const seq = parseInt(parts[2], 10);
    if (!run || !Number.isFinite(seq)) return null;
    const third = parts[3];
    if (third === 'entry') {
      const entryId = parts[4];
      const n = parseInt(parts[5], 10);
      if (!entryId || !Number.isFinite(n)) return null;
      return { kind: 'replay-entry', run, seq, entryId, n, tab: parts[6] || 'request' };
    }
    if (third === 'match') {
      let scope = 'matching';
      let candidate = null;
      if (parts[4] === 'matching' || parts[4] === 'all') {
        scope = parts[4];
        candidate = parts[5] || null;
      } else if (parts[4]) {
        candidate = parts[4];
      }
      return { kind: 'replay', run, seq, tab: 'match', scope, candidate };
    }
    const tab = third || 'match';
    if (!TABS.includes(tab)) return null;
    if (tab === 'match') return { kind: 'replay', run, seq, tab, scope: 'matching', candidate: null };
    return { kind: 'replay', run, seq, tab };
  }
  return null;
}

export function buildHash(route) {
  if (!route) return '#/';
  const enc = encodeURIComponent;
  switch (route.kind) {
    case 'entry':
      return `#/entry/${enc(route.id)}/${route.tab || 'request'}`;
    case 'replay': {
      const base = `#/replay/${enc(route.run)}/${route.seq}`;
      if (route.tab && route.tab !== 'match') return `${base}/${route.tab}`;
      if (route.scope || route.candidate) {
        const scope = route.scope || 'matching';
        return route.candidate
          ? `${base}/match/${scope}/${enc(route.candidate)}`
          : `${base}/match/${scope}`;
      }
      return base;
    }
    case 'replay-entry':
      return `#/replay/${enc(route.run)}/${route.seq}/entry/${enc(route.entryId)}/${route.n}/${route.tab || 'request'}`;
    default:
      return '#/';
  }
}
