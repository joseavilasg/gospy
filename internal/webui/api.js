import { requests, applyFullList, applyPage, applyListDiff, ignoredHosts, setIgnoredHosts, focusedHosts, setFocusedHosts, lastTimestamp, setLastTimestamp, criteriaVersion, setCriteriaVersion, rules, setRules, selectedId } from './state.js';
import { renderList, renderIgnoredList, renderFocusedList, renderRulesList, invalidateFilterCache, setReplayFeed, prependReplayFeed, clearReplayFeed } from './render.js';
import { syncCriteriaFromServer } from './filters.js';

let onSelectedUpdated = () => { };
let loadingMore = false;
let onReplayUpdate = () => { };
let onRecordingStoppedUpdate = () => { };

export function setOnSelectedUpdated(cb) { onSelectedUpdated = cb; }
export function setOnReplayUpdate(cb) { onReplayUpdate = cb; }
export function setOnRecordingStoppedUpdate(cb) { onRecordingStoppedUpdate = cb; }

export async function loadMore() {
  if (loadingMore) return;
  loadingMore = true;
  try {
    const resp = await fetch('/api/requests?offset=' + requests.length + '&limit=1000');
    const data = await resp.json();
    if (!data.entries) return;
    if (data.version !== criteriaVersion) return;
    applyPage(data);
    invalidateFilterCache();
    renderList();
  } catch (e) {
    console.error('Failed to load more:', e);
  } finally {
    loadingMore = false;
  }
}

export async function loadRequests() {
  try {
    const params = new URLSearchParams();
    if (lastTimestamp) params.set('since', lastTimestamp);
    if (criteriaVersion != null) params.set('version', criteriaVersion);
    const qs = params.toString();
    const resp = await fetch('/api/requests' + (qs ? '?' + qs : ''));
    const data = await resp.json();

    if (data.upserts) {
      if (data.version !== criteriaVersion) return;
      const selUpdated = selectedId && data.upserts.some(u => u.id === selectedId);
      applyListDiff(data);
      if (requests.length > 0) {
        setLastTimestamp(requests[0].updatedAt || requests[0].timestamp);
      }
      invalidateFilterCache();
      renderList();
      if (data.replay) onReplayUpdate(data.replay);
      onRecordingStoppedUpdate(data.recordingStopped || false, data.recordingMax || '', data.recordingSession || '');
      if (selUpdated) onSelectedUpdated(selectedId);
    } else if (data.entries) {
      const prevSel = selectedId ? requests.find(r => r.id === selectedId) : null;
      applyFullList(data);
      if (requests.length > 0) {
        setLastTimestamp(requests[0].updatedAt || requests[0].timestamp);
      }
      syncCriteriaFromServer(data.filters, data.focusEnabled, {
        preview: data.agentPreview,
        enabled: data.agentEnabled,
        exposed: data.agentExposed,
      });
      if (data.replay) onReplayUpdate(data.replay);
      onRecordingStoppedUpdate(data.recordingStopped || false, data.recordingMax || '', data.recordingSession || '');
      const currSel = selectedId ? requests.find(r => r.id === selectedId) : null;
      if (prevSel && currSel && prevSel.updatedAt !== currSel.updatedAt) onSelectedUpdated(selectedId);
    }
  } catch (e) {
    console.error('Failed to load requests:', e);
  }
}

export async function loadIgnored() {
  try {
    const resp = await fetch('/api/ignored');
    setIgnoredHosts(await resp.json());
    document.getElementById('ignoredCount').textContent = ignoredHosts.length;
    renderIgnoredList();
  } catch (e) {
    console.error('Failed to load ignored:', e);
  }
}

export async function loadFocused() {
  try {
    const resp = await fetch('/api/focused');
    setFocusedHosts(await resp.json());
    document.getElementById('focusedCount').textContent = focusedHosts.length;
    renderFocusedList();
  } catch (e) {
    console.error('Failed to load focused:', e);
  }
}

export async function confirmIgnoreHost(host) {
  if (!host) return;
  if (!confirm('Ignore all requests from ' + host + '?')) return;
  try {
    const resp = await fetch('/api/ignored', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ host })
    });
    if (!resp.ok) throw new Error('Server returned ' + resp.status);
    ignoredHosts.push(host);
    document.getElementById('ignoredCount').textContent = ignoredHosts.length;
    renderIgnoredList();
    const btn = document.querySelector('.btn-ignore-detail');
    if (btn) {
      btn.outerHTML = `<button class="btn-active" data-action="unignore" data-host="${escapeAttr(host)}"><svg width="12" height="12" viewBox="0 0 16 16"><polyline points="3,8 7,12 13,4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg> Remove ignore</button>`;
    }
    invalidateFilterCache();
    setLastTimestamp('');
    loadRequests();
  } catch (e) {
    console.error('Failed to ignore host:', e);
  }
}

export async function confirmUnignoreHost(host) {
  try {
    await fetch('/api/ignored/' + encodeURIComponent(host), { method: 'DELETE' });
    setIgnoredHosts(ignoredHosts.filter(h => h !== host));
    document.getElementById('ignoredCount').textContent = ignoredHosts.length;
    renderIgnoredList();
    const btn = document.querySelector('.btn-active[data-action="unignore"]');
    if (btn) {
      btn.outerHTML = `<button class="btn-ignore-detail" data-action="ignore" data-host="${escapeAttr(host)}"><svg width="12" height="12" viewBox="0 0 16 16"><circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" stroke-width="2"/><line x1="5" y1="5" x2="11" y2="11" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg> Ignore host</button>`;
    }
    invalidateFilterCache();
    setLastTimestamp('');
    loadRequests();
  } catch (e) {
    console.error('Failed to unignore host:', e);
  }
}

export async function confirmFocusHost(host) {
  if (!host) return;
  if (focusedHosts.includes(host)) return;
  try {
    const resp = await fetch('/api/focused', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ host })
    });
    if (!resp.ok) throw new Error('Server returned ' + resp.status);
    focusedHosts.push(host);
    document.getElementById('focusedCount').textContent = focusedHosts.length;
    renderFocusedList();
    const btn = document.querySelector('.btn-focus-detail');
    if (btn) {
      btn.outerHTML = `<button class="btn-active btn-focus-active" data-action="unfocus" data-host="${escapeAttr(host)}"><svg width="12" height="12" viewBox="0 0 16 16"><polyline points="3,8 7,12 13,4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg> Focused</button>`;
    }
    invalidateFilterCache();
    renderList();
  } catch (e) {
    console.error('Failed to focus host:', e);
  }
}

export async function confirmUnfocusHost(host) {
  try {
    await fetch('/api/focused/' + encodeURIComponent(host), { method: 'DELETE' });
    setFocusedHosts(focusedHosts.filter(h => h !== host));
    document.getElementById('focusedCount').textContent = focusedHosts.length;
    renderFocusedList();
    const btn = document.querySelector('.btn-active[data-action="unfocus"]');
    if (btn) {
      btn.outerHTML = `<button class="btn-focus-detail" data-action="focus" data-host="${escapeAttr(host)}"><svg width="12" height="12" viewBox="0 0 16 16"><circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" stroke-width="2"/><circle cx="8" cy="8" r="3" fill="currentColor"/></svg> Add to focus</button>`;
    }
    invalidateFilterCache();
    renderList();
  } catch (e) {
    console.error('Failed to unfocus host:', e);
  }
}

function escapeAttr(str) {
  if (!str) return '';
  return str.replace(/&/g, '&amp;').replace(/"/g, '&quot;');
}

export async function loadRules() {
  try {
    const resp = await fetch('/api/rules');
    setRules(await resp.json());
    document.getElementById('rulesCount').textContent = rules.length;
    renderRulesList();
  } catch (e) {
    console.error('Failed to load rules:', e);
  }
}

export async function createRule(rule) {
  try {
    const resp = await fetch('/api/rules', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(rule)
    });
    if (!resp.ok) throw new Error('Server returned ' + resp.status);
    const result = await resp.json();
    await loadRules();
    return result;
  } catch (e) {
    console.error('Failed to create rule:', e);
    return null;
  }
}

export async function updateRule(id, rule) {
  try {
    const resp = await fetch('/api/rules/' + encodeURIComponent(id), {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(rule)
    });
    if (!resp.ok) throw new Error('Server returned ' + resp.status);
    const updated = await resp.json();
    const idx = rules.findIndex(r => r.id === id);
    if (idx >= 0) rules[idx] = updated;
    renderRulesList();
    return updated;
  } catch (e) {
    console.error('Failed to update rule:', e);
    return null;
  }
}

export async function deleteRule(id) {
  try {
    await fetch('/api/rules/' + encodeURIComponent(id), { method: 'DELETE' });
    setRules(rules.filter(r => r.id !== id));
    document.getElementById('rulesCount').textContent = rules.length;
    renderRulesList();
  } catch (e) {
    console.error('Failed to delete rule:', e);
  }
}

export async function toggleRule(id) {
  try {
    const resp = await fetch('/api/rules/' + encodeURIComponent(id), { method: 'PATCH' });
    if (!resp.ok) throw new Error('Server returned ' + resp.status);
    const result = await resp.json();
    await loadRules();
    return result;
  } catch (e) {
    console.error('Failed to toggle rule:', e);
    return null;
  }
}

export async function checkMatch(method, host, urlPattern, excludeId) {
  try {
    const resp = await fetch('/api/rules/check-match', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ method, host, url_pattern: urlPattern, exclude_id: excludeId || '' })
    });
    if (!resp.ok) throw new Error('Server returned ' + resp.status);
    return await resp.json();
  } catch (e) {
    console.error('Failed to check match:', e);
    return [];
  }
}

export async function loadReplayRuns() {
  try {
    const resp = await fetch('/api/replay/runs');
    const data = await resp.json();
    return { runs: data.runs || [], session: data.session || '' };
  } catch (e) {
    console.error('Failed to load replay runs:', e);
    return { runs: [], session: '' };
  }
}

const FEED_PAGE = 200;
let feedRunId = null;
let feedLoadingOlder = false;

export async function loadReplayFeed(runId) {
  feedRunId = runId;
  if (!runId) { clearReplayFeed(); return; }
  try {
    const resp = await fetch(`/api/replay/events?run=${encodeURIComponent(runId)}&limit=${FEED_PAGE}`);
    const data = await resp.json();
    setReplayFeed(data.events || [], !!data.hasMore);
  } catch (e) {
    console.error('Failed to load replay feed:', e);
    clearReplayFeed();
  }
}

export async function loadReplayFeedOlder(beforeSeq) {
  if (!feedRunId || feedLoadingOlder || beforeSeq == null) return;
  feedLoadingOlder = true;
  try {
    const resp = await fetch(`/api/replay/events?run=${encodeURIComponent(feedRunId)}&limit=${FEED_PAGE}&beforeSeq=${beforeSeq}`);
    const data = await resp.json();
    prependReplayFeed(data.events || [], !!data.hasMore);
  } catch (e) {
    console.error('Failed to load older replay events:', e);
  } finally {
    feedLoadingOlder = false;
  }
}

export async function loadReplayEventDetail(runId, seq) {
  try {
    const resp = await fetch(`/api/replay/events/${encodeURIComponent(runId)}/${seq}`);
    return await resp.json();
  } catch (e) {
    console.error('Failed to load replay event detail:', e);
    return null;
  }
}

export async function loadReplayCandidates(runId, seq, scope, q) {
  try {
    const params = new URLSearchParams();
    params.set('scope', scope || 'matching');
    if (q) params.set('q', q);
    const resp = await fetch(`/api/replay/events/${encodeURIComponent(runId)}/${seq}/candidates?${params}`);
    return await resp.json();
  } catch (e) {
    console.error('Failed to load replay candidates:', e);
    return null;
  }
}

export async function loadReplayCandidateDiff(runId, seq, entryId) {
  try {
    const resp = await fetch(`/api/replay/events/${encodeURIComponent(runId)}/${seq}/candidates/${encodeURIComponent(entryId)}`);
    return await resp.json();
  } catch (e) {
    console.error('Failed to load replay candidate diff:', e);
    return null;
  }
}
