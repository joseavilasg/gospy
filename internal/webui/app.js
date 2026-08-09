import { setFilterText, setFocusEnabled, setAgentPreview, setAgentEnabled, setAgentExposed, agentExposed, applyFullList, setLastTimestamp, selectedId, requests, rules, setRules, setSignatureCache, visibleCount, getReplayMode, setReplayMode, setReplayServed, setReplayComplete, markReplayServed } from './state.js';
import { loadRequests, loadIgnored, loadFocused, confirmIgnoreHost, confirmUnignoreHost, confirmFocusHost, confirmUnfocusHost, loadRules, createRule, updateRule, deleteRule, toggleRule, checkMatch, setOnSelectedUpdated, loadMore, setOnReplayUpdate, loadReplayRuns, loadReplayFeed, loadReplayFeedOlder, loadReplayEventDetail, loadReplayCandidates, loadReplayCandidateDiff } from './api.js';
import { renderList, selectRequest, showTab, toggleIgnoredPanel, toggleFocusedPanel, toggleRulesPanel, toggleReplayPanel, renderRulesList, onListScroll, invalidateFilterCache, escapeHtml, SVG_EDIT, SVG_REVERT, SVG_MAXIMIZE, SVG_MINIMIZE, openRuleModal, closeRuleModal, openRuleModalFromRequest, buildResponseTab, ITEM_HEIGHT, appendReplayFeedEvent, onReplayFeedScroll, setOnReplayFeedLoadOlder, renderReplayEventDetail, renderReplayMatch, renderMatchCandidates, selectReplayFeedEvent, setReplayEntryView, renderUrlViewInner } from './render.js';
import { makeResizable } from './resize.js';
import { initHeader, setHeaderMode } from './header.js';
import { isBodySearching, cancelBodySearch, invalidateCriteriaSave, syncCriteriaFromServer, restoreBodyFilter, setOnFilterChange, setOnListRefresh, initFilterPopover, openFilterPopover, closeFilterPopover, closeChip, openChip, getFilterChipsData, getMatchMode, setMatchMode, queueCriteriaSave } from './filters.js';
import { initBodyTypes, editBody, saveBody, cancelBody, setBodyView, copyBody, getActiveEditor, postRenderBody } from './body-types.js';

let _pendingFullscreenTarget = null;
let _savedScrollTop = 0;
let _lastDetailEntry = null;
let _streamEventSource = null;
let _streamState = null; // { id, text, truncated, bodySize }

document.getElementById('filterInput').addEventListener('input', (e) => {
  setFilterText(e.target.value.trim());
  queueCriteriaSave();
  document.getElementById('requestList').scrollTop = 0;
  renderList();
});

// Header action bar - items rendered by header.js; separators are derived from
// the visible units, so replay mode can't leave orphaned palotes behind.
const refreshClick = () => {
  setLastTimestamp('');
  document.getElementById('requestList').scrollTop = 0;
  loadRequests();
};

const focusEnabledChange = (e) => {
  setFocusEnabled(e.target.checked);
  queueCriteriaSave();
  document.getElementById('requestList').scrollTop = 0;
};

const agentPreviewChange = async (e) => {
  const enabled = e.target.checked;
  cancelBodySearch();
  invalidateCriteriaSave();
  setAgentPreview(enabled);
  updateAgentBanner();
  try {
    const resp = await fetch('/api/agent/view', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ preview: enabled }),
    });
    const data = await resp.json();
    applyFullList(data);
    document.getElementById('requestList').scrollTop = 0;
    syncCriteriaFromServer(data.filters, data.focusEnabled, {
      preview: data.agentPreview,
      enabled: data.agentEnabled,
      exposed: data.agentExposed,
    });
  } catch (_) { }
};

const agentEnabledChange = async (e) => {
  const enabled = e.target.checked;
  setAgentEnabled(enabled);
  updateAgentBanner();
  try {
    const resp = await fetch('/api/agent/enabled', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ enabled }),
    });
    const data = await resp.json();
    setAgentEnabled(!!data.enabled);
    setAgentExposed(!!data.exposed);
    updateAgentBanner();
  } catch (_) { }
};

const replayChipClick = () => {
  toggleReplayPanel();
  if (document.getElementById('replayPanel').classList.contains('open')) {
    ensureReplayStream();
    const sel = document.getElementById('replayRunSelect');
    if (!sel.value) {
      populateReplayRuns().then(() => {
        if (!_activeRunId && _pickedRun === null && sel.value) renderFeedFor(sel.value);
      });
    }
    renderFeedFor(_pickedRun === null ? (_activeRunId || sel.value) : _pickedRun);
  }
};

initHeader('headerActions', [
  {
    id: 'focusBtn',
    hiddenIn: ['replay'],
    html: '<button class="btn" id="focusBtn">Focus <span class="badge" id="focusedCount">0</span></button>',
    events: { click: toggleFocusedPanel },
  },
  {
    id: 'focusEnabled',
    hiddenIn: ['replay'],
    sep: false,
    html: '<div class="auto-refresh"><input type="checkbox" id="focusEnabled" title="Enable focus filter"></div>',
    events: { change: focusEnabledChange },
  },
  {
    id: 'agentEnabledToggle',
    hiddenIn: ['replay'],
    html: '<label class="agent-view-toggle" id="agentEnabledToggle"><input type="checkbox" id="agentEnabled" title="Enable the agent MCP (resets to off on every start)"><span>Agent enabled</span></label>',
    events: { change: agentEnabledChange },
  },
  {
    id: 'agentPreviewToggle',
    hiddenIn: ['replay'],
    sep: false,
    html: '<label class="agent-view-toggle" id="agentPreviewToggle"><input type="checkbox" id="agentPreview" title="Agent view: preview the scope the agent MCP can see"><span>Agent view</span></label>',
    events: { change: agentPreviewChange },
  },
  {
    id: 'refreshBtn',
    html: '<button class="btn" id="refreshBtn">Refresh</button>',
    events: { click: refreshClick },
  },
  {
    id: 'autoRefresh',
    sep: false,
    html: '<div class="auto-refresh"><input type="checkbox" id="autoRefresh" checked title="Auto-refresh requests"></div>',
  },
  {
    id: 'ignoredBtn',
    hiddenIn: ['replay'],
    html: '<button class="btn" id="ignoredBtn">Ignored <span class="badge" id="ignoredCount">0</span></button>',
    events: { click: toggleIgnoredPanel },
  },
  {
    id: 'rulesBtn',
    hiddenIn: ['replay'],
    html: '<button class="btn" id="rulesBtn">Rules <span class="badge" id="rulesCount">0</span></button>',
    events: { click: toggleRulesPanel },
  },
  {
    id: 'replayChip',
    hiddenIn: ['normal'],
    html: '<button class="btn replay-chip" id="replayChip" title="Replay activity (Ctrl+J)"><span class="replay-chip-label">REPLAY</span> <span class="badge" id="replayChipProgress">0/0</span><span class="replay-chip-exhausted" id="replayChipExhausted" style="display:none">EXHAUSTED</span></button>',
    events: { click: replayChipClick },
  },
]);

// ── Replay mode (read-only UI over a session) ─────────────────────────────
let _replayStreamSrc = null;
let _activeRunId = null;
let _pickedRun = null;

function updateReplayChip(rp) {
  const progress = document.getElementById('replayChipProgress');
  const exhausted = document.getElementById('replayChipExhausted');
  if (!rp || !rp.active) {
    progress.textContent = '0/0';
    exhausted.style.display = 'none';
    return;
  }
  progress.textContent = `${rp.consumed}/${rp.total}`;
  exhausted.style.display = rp.exhausted ? '' : 'none';
}

function applyReplayLayout() {
  if (document.body.dataset.replayLayout) return;
  document.body.dataset.replayLayout = '1';
  setHeaderMode('replay');
  const banner = document.getElementById('agentBanner');
  if (banner) banner.style.display = 'none';
}

function renderFeedFor(runId) {
  loadReplayFeed(runId);
}

function syncReplay(rp) {
  setReplayMode(!!rp);
  if (!rp) {
    closeReplayStream();
    _activeRunId = null;
    setHeaderMode('normal');
    document.getElementById('replayPanel').classList.remove('open');
    return;
  }
  applyReplayLayout();
  updateReplayChip(rp);
  setReplayServed(new Set(rp.served || []));
  setReplayComplete(!rp.active && !!rp.runId);
  if (!_activeRunId && rp.active && rp.runId) _activeRunId = rp.runId;
  renderList();
  ensureReplayStream();
}

function closeReplayStream() {
  if (_replayStreamSrc) {
    _replayStreamSrc.close();
    _replayStreamSrc = null;
  }
}

function connectReplayStream() {
  if (_replayStreamSrc && _replayStreamSrc.readyState !== EventSource.CLOSED) return;
  const src = new EventSource('/api/replay/events/stream');
  _replayStreamSrc = src;
  src.onmessage = (e) => {
    try {
      const ev = JSON.parse(e.data);
      if (ev.type === 'runChanged') {
        if (!ev.runId) return;
        _activeRunId = ev.runId;
        _pickedRun = null;
        populateReplayRuns().then(() => {
          const sel = document.getElementById('replayRunSelect');
          if (sel && [...sel.options].some(o => o.value === _activeRunId)) sel.value = _activeRunId;
          updateReplayRunMeta(_activeRunId);
        });
        renderFeedFor(_activeRunId);
        return;
      }
      if (ev.result === 'hit' && ev.entryId) {
        markReplayServed(ev.entryId);
        renderList();
      }
      updateReplayChip({ active: true, consumed: ev.consumed, total: ev.total, exhausted: ev.exhausted });
      if (_pickedRun === null && ev.runId === _activeRunId) appendReplayFeedEvent(ev);
    } catch (err) { }
  };
}

function ensureReplayStream() {
  if (getReplayMode() && (!_replayStreamSrc || _replayStreamSrc.readyState === EventSource.CLOSED)) {
    connectReplayStream();
  }
}

setInterval(() => {
  ensureReplayStream();
}, 3000);

let _replayRuns = [];

function runTimeLabel(ts) {
  return new Date(ts).toTimeString().slice(0, 8);
}

function pluralLabel(n, word) {
  const pluralSuffix = /[sxz]$/.test(word) || /(ch|sh)$/.test(word) ? 'es' : 's';
  return `${n} ${n === 1 ? word : word + pluralSuffix}`;
}

function runStatsLabel(r) {
  const base = `${pluralLabel(r.hits, 'hit')} · ${pluralLabel(r.misses, 'miss')}`;
  return r.exhausted > 0 ? `${base} · ${r.exhausted} exhausted` : base;
}

function updateReplayRunMeta(runId) {
  const meta = document.getElementById('replayRunMeta');
  if (!meta) return;
  const r = _replayRuns.find(x => x.runId === runId);
  meta.textContent = r
    ? `${r.runId} · ${pluralLabel(r.hits, 'hit')} · ${pluralLabel(r.misses, 'miss')} · ${r.exhausted} exhausted · ${((r.durationMs || 0) / 1000).toFixed(1)}s`
    : '';
}

function populateReplayRuns() {
  return loadReplayRuns().then(({ runs, session }) => {
    _replayRuns = runs;
    const label = document.getElementById('replaySessionLabel');
    if (label) label.textContent = session || '';
    const sel = document.getElementById('replayRunSelect');
    if (!sel) return;
    const prev = sel.value;
    sel.innerHTML = runs.length === 0
      ? '<option value="">No runs yet</option>'
      : runs.map(r => `<option value="${escapeHtml(r.runId)}">${escapeHtml(runTimeLabel(r.ts))} · ${escapeHtml(runStatsLabel(r))}</option>`).join('');
    if (prev && [...sel.options].some(o => o.value === prev)) sel.value = prev;
    if (runs.length > 0 && !sel.value) sel.value = runs[0].runId;
    updateReplayRunMeta(sel.value);
  });
}

setOnReplayUpdate(syncReplay);
setOnReplayFeedLoadOlder(beforeSeq => loadReplayFeedOlder(beforeSeq));
let _lastReplayDetail = null;
let _matchState = null;
let _matchResp = null;
let _matchEventCtx = null;
let _matchSearchTimer = null;
let _matchQueries = {};

function currentMatchQuery() {
  const input = document.querySelector('.match-search');
  return input ? input.value : (_matchState && _matchState.q ? _matchState.q : '');
}

function loadMatchTab(run, seq, scope, q, rowsOnly) {
  if (!_matchState || _matchState.run !== run || _matchState.seq !== seq) {
    _matchQueries = {};
  }
  _matchState = { run, seq, scope, q: q || '' };
  _matchEventCtx = { result: (_lastReplayDetail && _lastReplayDetail.event ? _lastReplayDetail.event.result : '') || '', seq };
  loadReplayCandidates(run, seq, scope, q || '').then(resp => {
    if (scope === 'matching' && resp && resp.total && resp.total.matching === 0 && !(resp.entries && resp.entries.length)) {
      _matchState.scope = 'all';
      _matchQueries['all'] = q || '';
      loadMatchTab(run, seq, 'all', q || '');
      return;
    }
    _matchResp = { ...resp, q: q || '' };
    if (rowsOnly) renderMatchCandidates(_matchResp, _matchEventCtx);
    else renderReplayMatch(_matchResp, _matchEventCtx);
  });
}

function selectMatchCandidate(entryId) {
  if (!_matchResp || !_matchState) return;
  if (!_matchResp.entries.some(e => e.entryId === entryId)) return;
  loadReplayCandidateDiff(_matchState.run, _matchState.seq, entryId).then(dr => {
    const resp = { ..._matchResp, q: currentMatchQuery(), selectedEntryId: entryId, diff: dr && dr.diff ? dr.diff : null };
    renderReplayMatch(resp, _matchEventCtx, true);
  });
}

function showReplayDetail(detail) {
  _lastReplayDetail = detail;
  _lastDetailEntry = null;
  renderReplayEventDetail(detail);
}

document.getElementById('replayPanel').addEventListener('click', (e) => {
  if (e.target.closest('.ignored-panel-close')) { toggleReplayPanel(); return; }
  const item = e.target.closest('[data-action="replay-event-detail"]');
  if (item) {
    selectReplayFeedEvent(item.dataset.run, parseInt(item.dataset.seq, 10));
    loadReplayEventDetail(item.dataset.run, parseInt(item.dataset.seq, 10)).then(detail => {
      showReplayDetail(detail);
      if (detail && detail.event) {
        loadMatchTab(detail.event.runId, detail.event.seq, 'matching', '');
      }
    });
  }
});
document.getElementById('replayRunSelect').addEventListener('change', (e) => {
  const runId = e.target.value;
  _pickedRun = runId || null;
  updateReplayRunMeta(runId);
  renderFeedFor(_pickedRun);
});

makeResizable(document.getElementById('replayDrag'), document.getElementById('replayPanel'), {
  persistKey: 'gospy-replay-panel-h',
  min: 120,
});

document.getElementById('focusAddBtn').addEventListener('click', () => {
  const input = document.getElementById('focusInput');
  const pattern = input.value.trim();
  if (pattern) {
    confirmFocusHost(pattern);
    input.value = '';
  }
});

document.getElementById('focusInput').addEventListener('keydown', (e) => {
  if (e.key === 'Enter') {
    const input = e.target;
    const pattern = input.value.trim();
    if (pattern) {
      confirmFocusHost(pattern);
      input.value = '';
    }
  }
});

document.getElementById('ignoreAddBtn').addEventListener('click', () => {
  const input = document.getElementById('ignoreInput');
  const pattern = input.value.trim();
  if (pattern) {
    confirmIgnoreHost(pattern);
    input.value = '';
  }
});

document.getElementById('ignoreInput').addEventListener('keydown', (e) => {
  if (e.key === 'Enter') {
    const input = e.target;
    const pattern = input.value.trim();
    if (pattern) {
      confirmIgnoreHost(pattern);
      input.value = '';
    }
  }
});

document.getElementById('ignoredPanel').addEventListener('click', (e) => {
  if (e.target.closest('.ignored-panel-close')) {
    toggleIgnoredPanel();
    return;
  }
  const btn = e.target.closest('[data-action="unignore-item"]');
  if (btn) {
    confirmUnignoreHost(btn.dataset.host);
  }
});

document.getElementById('focusedPanel').addEventListener('click', (e) => {
  if (e.target.closest('.ignored-panel-close')) {
    toggleFocusedPanel();
    return;
  }
  const btn = e.target.closest('[data-action="unfocus-item"]');
  if (btn) {
    confirmUnfocusHost(btn.dataset.host);
  }
});

document.getElementById('rulesPanel').addEventListener('click', (e) => {
  if (e.target.closest('.ignored-panel-close')) {
    toggleRulesPanel();
    return;
  }
  if (e.target.closest('#addRuleBtn')) {
    openRuleModal(null);
    return;
  }
  const toggleBtn = e.target.closest('[data-action="toggle-rule"]');
  if (toggleBtn) {
    const ruleId = toggleBtn.dataset.ruleId;
    const rule = rules.find(r => r.id === ruleId);
    if (rule && !rule.enabled) {
      const matches = rules.filter(r => r.id !== ruleId && r.enabled &&
        r.match.method === rule.match.method &&
        r.match.host === rule.match.host &&
        r.match.url_pattern === rule.match.url_pattern);
      if (matches.length > 0) {
        if (!confirm(`Activating this rule will deactivate "${matches[0].name}" which has the same match. Continue?`)) return;
      }
    }
    toggleRule(ruleId);
    return;
  }
  const editBtn = e.target.closest('[data-action="edit-rule"]');
  if (editBtn) {
    const rule = rules.find(r => r.id === editBtn.dataset.ruleId);
    if (rule) openRuleModal(rule);
    return;
  }
  const deleteBtn = e.target.closest('[data-action="delete-rule"]');
  if (deleteBtn) {
    if (confirm('Delete this rule?')) {
      deleteRule(deleteBtn.dataset.ruleId);
    }
    return;
  }
});

document.getElementById('requestList').addEventListener('click', (e) => {
  const item = e.target.closest('.request-item');
  if (item && item.dataset.id) {
    selectRequest(item.dataset.id);
  }
});

document.getElementById('detailPanel').addEventListener('input', (e) => {
  if (!e.target.dataset || e.target.dataset.action !== 'replay-search') return;
  const wrap = e.target.closest('.match-search-wrap');
  if (wrap) wrap.querySelector('.match-search-clear')?.classList.toggle('hidden', !e.target.value);
  clearTimeout(_matchSearchTimer);
  _matchSearchTimer = setTimeout(() => {
    if (_matchState) {
      _matchQueries[_matchState.scope] = e.target.value || '';
      loadMatchTab(_matchState.run, _matchState.seq, _matchState.scope, e.target.value || '', true);
    }
  }, 250);
});

document.getElementById('detailPanel').addEventListener('click', (e) => {
  const btn = e.target.closest('[data-action]');
  if (!btn) return;
  switch (btn.dataset.action) {
    case 'toggle-menu':
      toggleKebabMenu(btn.closest('.kebab'));
      break;
    case 'ignore':
      confirmIgnoreHost(btn.dataset.host);
      break;
    case 'unignore':
      confirmUnignoreHost(btn.dataset.host);
      break;
    case 'focus':
      confirmFocusHost(btn.dataset.host);
      break;
    case 'unfocus':
      confirmUnfocusHost(btn.dataset.host);
      break;
    case 'tab':
      showTab(btn, btn.dataset.tab);
      syncStreamView(btn.dataset.tab);
      break;
    case 'set-view':
      setBodyView(btn.dataset.target, btn.dataset.view);
      break;
    case 'set-content':
      setContent(btn.dataset.target, btn.dataset.content);
      break;
    case 'copy-body':
    case 'copy-hex':
      copyBody(btn.dataset.target);
      break;
    case 'download-bin':
      downloadBin(btn.dataset.target, btn.dataset.entryId);
      break;
    case 'copy-curl':
      copyCurl();
      break;
    case 'copy-headers':
      copyHeaders(btn.dataset.target);
      break;
    case 'edit-body':
      editBody(btn.dataset.target);
      break;
    case 'save-body':
      saveBody(btn.dataset.target);
      break;
    case 'cancel-body':
      cancelBody(btn.dataset.target);
      break;
    case 'send-replay':
      sendReplay();
      break;
    case 'create-rule-from-request':
      createRuleFromRequest();
      break;
    case 'revert-body':
      revertBody(btn.dataset.target);
      break;
    case 'goto-replay':
      selectRequest(btn.dataset.id);
      break;
    case 'replay-body':
      fetch(`/api/replay/events/${encodeURIComponent(btn.dataset.run)}/${btn.dataset.seq}/body`)
        .then(r => r.text())
        .then(text => {
          const pre = document.querySelector('.detail-panel .body-viewer-body pre');
          if (pre) pre.textContent = text;
        })
        .catch(e => console.error('Failed to load replay body:', e));
      break;
    case 'replay-entry-body':
      downloadBin(btn.dataset.target || 'response', btn.dataset.id);
      break;
    case 'replay-full-entry': {
      const c = _matchResp && _matchResp.selectedEntryId
        ? _matchResp.entries.find(e => e.entryId === _matchResp.selectedEntryId)
        : null;
      if (c && _matchState) {
        setReplayEntryView({ seq: _matchState.seq, entry: c.entry });
        selectRequest(c.entryId);
      }
      break;
    }
    case 'replay-warn-entry': {
      const ci = _matchResp && _matchResp.consumed;
      if (ci && _matchState) {
        setReplayEntryView({ seq: _matchState.seq, entry: ci.entry });
        selectRequest(ci.entryId);
      }
      break;
    }
    case 'replay-back-event': {
      const ev = _lastReplayDetail && _lastReplayDetail.event;
      if (ev) {
        selectReplayFeedEvent(ev.runId, ev.seq);
        showReplayDetail(_lastReplayDetail);
        loadMatchTab(ev.runId, ev.seq, 'matching', '');
      }
      break;
    }
    case 'replay-candidate':
      selectMatchCandidate(btn.dataset.entry);
      break;
    case 'replay-scope': {
      if (!_matchState) break;
      clearTimeout(_matchSearchTimer);
      const input = document.querySelector('.match-search');
      if (input) _matchQueries[_matchState.scope] = input.value;
      const scope = btn.dataset.scope;
      loadMatchTab(_matchState.run, _matchState.seq, scope, _matchQueries[scope] || '');
      break;
    }
    case 'replay-search-clear': {
      const wrap = btn.closest('.match-search-wrap');
      const input = wrap?.querySelector('.match-search');
      if (input) {
        input.value = '';
        btn.classList.add('hidden');
        if (_matchState) {
          _matchQueries[_matchState.scope] = '';
          loadMatchTab(_matchState.run, _matchState.seq, _matchState.scope, '', true);
        }
        input.focus();
      }
      break;
    }
    case 'copy-id': {
      const idSpan = btn.closest('.detail-id-group')?.querySelector('.detail-id');
      if (idSpan) {
        navigator.clipboard.writeText(idSpan.textContent).then(() => {
          btn.classList.add('copied');
          setTimeout(() => btn.classList.remove('copied'), 1500);
        });
      }
      break;
    }
    case 'toggle-replays':
      const replaysList = btn.closest('.replays-section').querySelector('.replays-list');
      if (replaysList) replaysList.classList.toggle('collapsed');
      const toggle = btn.querySelector('.replays-toggle');
      if (toggle) toggle.textContent = replaysList.classList.contains('collapsed') ? '▸' : '▾';
      break;
    case 'toggle-fullscreen-body':
      toggleFullscreenBody(btn.dataset.target, btn);
      break;
    case 'edit-headers':
      editHeaders(btn.closest('.tab-content')?.querySelector('.headers-container'));
      break;
    case 'save-headers':
      saveHeaders(selectedId);
      break;
    case 'cancel-headers':
      cancelHeadersEdit(btn.closest('.headers-container'));
      break;
    case 'add-header':
      addHeaderRow(btn.closest('.headers-container'));
      break;
    case 'remove-header':
      removeHeaderRow(btn);
      break;
    case 'set-url-view': {
      const view = btn.dataset.view;
      const urlView = btn.closest('.url-view');
      if (!urlView) break;
      urlView.dataset.viewMode = view;
      urlView.innerHTML = renderUrlViewInner(urlView.dataset.method, urlView.dataset.urlOriginal, urlView.dataset.urlModified, view, urlView.dataset.contentMode);
      break;
    }
    case 'set-url-content': {
      const mode = btn.dataset.content;
      const urlView = btn.closest('.url-view');
      if (!urlView) break;
      urlView.dataset.contentMode = mode;
      urlView.innerHTML = renderUrlViewInner(urlView.dataset.method, urlView.dataset.urlOriginal, urlView.dataset.urlModified, urlView.dataset.viewMode, mode);
      break;
    }
    case 'set-header-content': {
      const mode = btn.dataset.content;
      const target = btn.dataset.target || 'request';
      const container = btn.closest('.tab-content')?.querySelector(`.headers-container[data-target="${target}"]`);
      if (!container) break;
      if (mode === 'original') {
        container.innerHTML = container.dataset.originalHtml;
      } else if (mode === 'mocked' && container.dataset.mockedHtml) {
        container.innerHTML = container.dataset.mockedHtml;
      } else if (mode === 'modified' && container.dataset.modifiedHtml) {
        container.innerHTML = container.dataset.modifiedHtml;
      } else {
        container.innerHTML = container.dataset.editedHtml || container.dataset.originalHtml;
      }
      container.dataset.headerMode = mode;
      btn.closest('.body-tools-group')?.querySelectorAll('.body-tool').forEach(b => b.classList.remove('active'));
      btn.classList.add('active');
      break;
    }
    case 'revert-headers':
      if (!selectedId) break;
      fetch(`/api/requests/${selectedId}/headers`, { method: 'DELETE' })
        .then(r => r.json()).then(() => refreshDetail())
        .catch(e => console.error('Revert headers failed:', e));
      break;
  }
  if (btn.dataset.action !== 'toggle-menu') {
    closeAllKebabMenus();
  }
});

function updateResponseInPlace(entry) {
  const tab = document.getElementById('tab-response');
  if (!tab) return;
  if (entry.response && entry.response.stream === true) {
    // A live stream owns its body through the SSE view: rebuilding the
    // tab here would reset the viewer, the view toggle, the kebab menus
    // and the live badge on every checkpoint. Keep the DOM and make sure
    // the live view is open when the user is already on the Response tab.
    _lastDetailEntry = entry;
    const activeTabEl = document.querySelector('.tab.active');
    syncStreamView(activeTabEl ? activeTabEl.dataset.tab : 'request');
    return;
  }
  const wasFullscreen = !!document.querySelector('.section-panel.fullscreen-mode[data-body-target="response"]');
  tab.innerHTML = buildResponseTab(entry);
  renderCurrentContent('response');
  postRenderBody('response');
  if (wasFullscreen) {
    const panel = tab.querySelector('.section-panel[data-body-target="response"]');
    const btn = panel?.querySelector('[data-action="toggle-fullscreen-body"]');
    if (panel && btn) enterFullscreenBody(panel, btn);
  }
}

document.getElementById('detailPanel').addEventListener('detail-rendered', (e) => {
  _lastDetailEntry = e.detail?.entry || _lastDetailEntry;
  renderCurrentContent('request');
  renderCurrentContent('response');
  postRenderBody('request');
  postRenderBody('response');
  syncStreamView(e.detail?.activeTab || 'request');
  if (_pendingFullscreenTarget) {
    const panel = document.querySelector(`.section-panel[data-body-target="${_pendingFullscreenTarget}"]`);
    const btn = panel?.querySelector('[data-action="toggle-fullscreen-body"]');
    if (panel && btn) enterFullscreenBody(panel, btn);
    _pendingFullscreenTarget = null;
  }
});

// syncStreamView keeps the SSE subscription for a live response body aligned
// with what is actually visible: it only streams while the Response tab is
// active on a streaming entry, so no traffic flows until the user looks at the
// response.
function syncStreamView(tab) {
  const entry = _lastDetailEntry;
  const streaming = tab === 'response' && entry && entry.response && entry.response.stream === true && entry.response.bodyFile;
  if (!streaming) {
    closeStreamView();
    return;
  }
  if (_streamState && _streamState.id === entry.id && _streamEventSource) return;
  openStreamView(entry);
}

function openStreamView(entry) {
  closeStreamView();
  _streamState = { id: entry.id, text: '', truncated: false, bodySize: 0, type: '', delta: '', markerAppended: false };
  _streamEventSource = new EventSource(`/api/streams/${entry.id}/events`);
  _streamEventSource.onmessage = (e) => {
    let ev;
    try { ev = JSON.parse(e.data); } catch { return; }
    handleStreamEvent(ev);
  };
  _streamEventSource.onerror = () => {
    const id = _streamState ? _streamState.id : null;
    closeStreamView();
    if (id && selectedId === id) refreshDetail();
  };
}

function closeStreamView() {
  if (_streamEventSource) {
    _streamEventSource.close();
    _streamEventSource = null;
  }
  _streamState = null;
}

function handleStreamEvent(ev) {
  const st = _streamState;
  if (!st) return;
  if (ev.type === 'snapshot') {
    st.type = 'snapshot';
    st.delta = '';
    st.text = ev.preview || '';
    st.truncated = !!ev.truncated;
    st.bodySize = ev.bodySize || 0;
    applyStreamBody();
  } else if (ev.type === 'update') {
    st.type = 'update';
    st.delta = ev.preview || '';
    st.text += st.delta;
    st.truncated = !!ev.truncated;
    st.bodySize = ev.bodySize || 0;
    applyStreamBody();
  } else if (ev.type === 'done') {
    const id = st.id;
    closeStreamView();
    if (selectedId === id) refreshDetail();
  }
}

function applyStreamBody() {
  const st = _streamState;
  if (!st) return;
  const pre = document.querySelector('pre[data-body-target="response"]');
  if (!pre || pre.dataset.binary || pre.dataset.multipart) return;

  pre.dataset.decoded = st.text;
  pre.dataset.raw = st.text;

  // Pretty mode re-renders the whole body (JSON tree rebuild); keep the old
  // behavior there. Raw text bodies grow incrementally below, so the viewer,
  // the view toggle, the kebab menus and the live badge keep their state
  // across deltas and the DOM work stays O(1) per event.
  if (pre.dataset.viewMode === 'pretty') {
    renderCurrentContent('response');
    return;
  }

  const wasPinned = pre.scrollTop + pre.clientHeight >= pre.scrollHeight - 1;

  if (st.type === 'snapshot') {
    st.markerAppended = false;
    pre.textContent = st.text;
  } else if (st.delta) {
    pre.appendChild(document.createTextNode(st.delta));
  }

  if (st.truncated && !st.markerAppended) {
    pre.appendChild(document.createTextNode('\n... [truncated - body too large]'));
    st.markerAppended = true;
  }

  if (wasPinned) pre.scrollTop = pre.scrollHeight;
}

function setContent(target, content) {
  const pre = document.querySelector(`pre[data-body-target="${target}"]`);
  if (!pre) return;
  pre.dataset.contentMode = content;
  const sectionPanel = pre.closest('.section-panel');
  if (sectionPanel) {
    sectionPanel.querySelectorAll('[data-action="set-content"]').forEach(b => {
      b.classList.toggle('active', b.dataset.content === content);
    });
  }
  renderCurrentContent(target);
}

function toggleFullscreenBody(target, btn) {
  const panel = document.querySelector(`.section-panel[data-body-target="${target}"]`);
  if (!panel) return;
  if (panel.classList.contains('fullscreen-mode')) {
    exitFullscreenBody(panel, btn);
  } else {
    enterFullscreenBody(panel, btn);
  }
}

function enterFullscreenBody(panel, btn) {
  const scrollContainer = document.querySelector('.detail-panel');
  _savedScrollTop = scrollContainer?.scrollTop || 0;

  panel.classList.add('fullscreen-mode');
  document.body.classList.add('fullscreen-active');

  const kebab = panel.querySelector('.section-header .kebab');
  const toolbarRight = panel.querySelector('.toolbar-right');
  if (kebab && toolbarRight && !toolbarRight.querySelector('.kebab-clone')) {
    const clone = kebab.cloneNode(true);
    clone.classList.add('kebab-clone');
    toolbarRight.appendChild(clone);
  }

  btn.innerHTML = SVG_MINIMIZE;
  btn.title = 'Exit full screen';

  document.removeEventListener('keydown', onFullscreenEsc);
  document.addEventListener('keydown', onFullscreenEsc);
}

function exitFullscreenBody(panel, btn) {
  panel.classList.remove('fullscreen-mode');
  document.body.classList.remove('fullscreen-active');

  const clone = panel.querySelector('.kebab-clone');
  if (clone) clone.remove();

  btn.innerHTML = SVG_MAXIMIZE;
  btn.title = 'Full screen';

  document.removeEventListener('keydown', onFullscreenEsc);

  const scrollContainer = document.querySelector('.detail-panel');
  if (scrollContainer) scrollContainer.scrollTop = _savedScrollTop;
}

function onFullscreenEsc(e) {
  if (e.key !== 'Escape') return;
  const panel = document.querySelector('.section-panel.fullscreen-mode');
  if (!panel) return;
  const btn = panel.querySelector('[data-action="toggle-fullscreen-body"]');
  if (btn) exitFullscreenBody(panel, btn);
}

function renderCurrentContent(target) {
  const pre = document.querySelector(`pre[data-body-target="${target}"]`);
  if (!pre || pre.dataset.binary || pre.dataset.multipart) return;
  const sectionPanel = pre.closest('.section-panel');
  if (!sectionPanel) return;

  const contentMode = pre.dataset.contentMode || 'original';
  const viewMode = pre.dataset.viewMode || 'raw';

  let content;
  switch (contentMode) {
    case 'edited': content = pre.dataset.edited || ''; break;
    case 'modified': content = pre.dataset.modified || ''; break;
    case 'mocked': content = pre.dataset.mocked || ''; break;
    default: content = pre.dataset.decoded || pre.dataset.raw || ''; break;
  }

  const contentBlock = sectionPanel.querySelector('.content-block');
  const bodyScroll = sectionPanel.querySelector('.body-scroll');
  const parentEl = bodyScroll || contentBlock;
  const existingTree = parentEl?.querySelector('.json-viewer-container');
  if (existingTree) existingTree.remove();

  if (viewMode === 'pretty') {
    try {
      const obj = JSON.parse(content);
      const container = document.createElement('div');
      container.className = 'json-viewer-container';
      if (parentEl) parentEl.appendChild(container);
      const jsonViewer = new JSONViewer();
      container.appendChild(jsonViewer.getContainer());
      jsonViewer.showJSON(obj, -1, 1);
      pre.style.display = 'none';
    } catch (e) {
      pre.textContent = content || '[not valid JSON]';
      pre.style.display = '';
    }
  } else {
    pre.textContent = content || '[no data]';
    pre.style.display = '';
  }
}

function downloadBin(target, entryId) {
  if (!entryId || !target) return;
  const a = document.createElement('a');
  a.href = `/api/requests/${entryId}/body-bin?target=${target}`;
  a.download = '';
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
}

function copyCurl() {
  if (!selectedId) return;
  const proxyHost = window.location.origin;
  fetch(`/api/requests/${selectedId}/curl?proxyHost=${encodeURIComponent(proxyHost)}`)
    .then(r => r.text())
    .then(text => navigator.clipboard.writeText(text));
}

function copyHeaders(target) {
  const container = document.querySelector(`.headers-container[data-target="${target}"]`);
  if (!container) return;

  const rows = container.querySelectorAll('.header-row');
  if (rows.length === 0) return;

  const lines = [];
  rows.forEach(row => {
    const key = row.dataset.key || row.querySelector('.header-key')?.textContent?.replace(/:$/, '') || '';
    const values = row.dataset.values ? JSON.parse(row.dataset.values) : [row.querySelector('.header-value')?.textContent || ''];
    values.forEach(v => lines.push(key + ': ' + v));
  });

  navigator.clipboard.writeText(lines.join('\n'));
}

let modalMonacoEditors = { modifyBody: null, mockReqBody: null, mockRespBody: null };

function disposeModalMonacoEditors() {
  for (const key of Object.keys(modalMonacoEditors)) {
    if (modalMonacoEditors[key]) {
      modalMonacoEditors[key].dispose();
      modalMonacoEditors[key] = null;
    }
  }
}

function initModalMonaco(containerId, value, editorKey) {
  const container = document.getElementById(containerId);
  if (!container) return;
  if (modalMonacoEditors[editorKey]) {
    modalMonacoEditors[editorKey].dispose();
    modalMonacoEditors[editorKey] = null;
  }
  const lang = mapContentType('application/json');
  createMonacoEditor(container, value || '', lang).then(editor => {
    modalMonacoEditors[editorKey] = editor;
  });
}

function mapContentType(ct) {
  if (!ct) return 'json';
  const lower = ct.toLowerCase();
  if (lower.includes('json')) return 'json';
  if (lower.includes('html')) return 'html';
  if (lower.includes('css')) return 'css';
  if (lower.includes('javascript') || lower.includes('ecmascript')) return 'javascript';
  if (lower.includes('xml')) return 'xml';
  if (lower.includes('yaml') || lower.includes('yml')) return 'yaml';
  if (lower.includes('sql')) return 'sql';
  if (lower.includes('python')) return 'python';
  return 'plaintext';
}

function sendReplay() {
  if (!confirm('Send replay? This will execute the request and create a new entry.')) return;

  let body = '';
  const editor = getActiveEditor();
  if (editor) {
    body = editor.getValue();
  } else {
    const pre = document.querySelector('pre[data-body-target="request"]');
    if (pre) body = pre.dataset.edited || pre.dataset.decoded || '';
  }

  fetch(`/api/requests/${selectedId}/replay`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ body })
  }).then(r => r.json()).then(({ id }) => {
    if (getActiveEditor()) cancelBody('request');
    setLastTimestamp('');
    loadRequests().then(() => selectRequest(id));
  }).catch(e => console.error('Replay failed:', e));
}

function revertBody(target) {
  const wasFullscreen = document.querySelector(`.section-panel[data-body-target="${target}"].fullscreen-mode`);
  _pendingFullscreenTarget = wasFullscreen ? target : null;
  fetch(`/api/requests/${selectedId}/body?target=${target}`, {
    method: 'DELETE'
  }).then(r => r.json()).then(() => {
    refreshDetail();
  }).catch(e => console.error('Revert failed:', e));
}

function refreshDetail() {
  if (selectedId) {
    const activeTabEl = document.querySelector('.tab.active');
    const tab = activeTabEl ? activeTabEl.dataset.tab : 'request';
    selectRequest(selectedId, tab);
  }
}


let scrollRAF = null;
document.getElementById('requestList').addEventListener('scroll', () => {
  if (scrollRAF) return;
  scrollRAF = requestAnimationFrame(() => {
    onListScroll();
    scrollRAF = null;
    maybeLoadMore();
  });
});

let feedScrollRAF = null;
document.getElementById('replayFeed').addEventListener('scroll', () => {
  if (feedScrollRAF) return;
  feedScrollRAF = requestAnimationFrame(() => {
    onReplayFeedScroll();
    feedScrollRAF = null;
  });
});

function maybeLoadMore() {
  if (requests.length >= visibleCount) return;
  const list = document.getElementById('requestList');
  if (list.scrollTop + list.clientHeight >= requests.length * ITEM_HEIGHT * 0.5) {
    loadMore();
  }
}

let savedListScrollTop = 0;
document.getElementById('toggleListBtn').addEventListener('click', () => {
  const container = document.getElementById('container');
  const list = document.getElementById('requestList');
  const willHide = !container.classList.contains('list-hidden');
  if (willHide) {
    savedListScrollTop = list.scrollTop;
  }
  container.classList.toggle('list-hidden');
  if (!willHide) {
    list.scrollTop = savedListScrollTop;
    onListScroll();
  }
  document.getElementById('toggleListBtn').classList.toggle('active', !willHide);
  localStorage.setItem('gospy-list-hidden', willHide);
});

const listHidden = localStorage.getItem('gospy-list-hidden') === 'true';
document.getElementById('container').classList.toggle('list-hidden', listHidden);
document.getElementById('toggleListBtn').classList.toggle('active', !listHidden);

loadRequests().then(() => {
  if (getReplayMode()) return;
  loadIgnored();
  loadFocused();
  loadRules();
  connectSSE();
});
restoreBodyFilter();
setInterval(() => {
  if (document.getElementById('autoRefresh').checked) { loadRequests(); }
}, 2000);

function createRuleFromRequest() {
  if (!selectedId) return;
  fetch(`/api/request-rule?id=${selectedId}`)
    .then(r => r.json())
    .then(entry => openRuleModalFromRequest(entry))
    .catch(e => console.error('Failed to load request for rule creation:', e));
}

function editHeaders(container) {
  if (!container || container.dataset.editing === 'true') return;
  container.dataset.editing = 'true';
  container.dataset.original = container.innerHTML;

  container.querySelectorAll('.header-row').forEach(row => {
    const key = row.dataset.key || '';
    const values = JSON.parse(row.dataset.values || '[]');
    const val = values.join(', ');
    row.innerHTML = `<input class="header-key-input" value="${escapeHtml(key)}" /><span class="header-colon">:</span><input class="header-value-input" value="${escapeHtml(val)}" /><button class="header-remove" data-action="remove-header" title="Remove">&times;</button>`;
  });

  const toolbar = document.createElement('div');
  toolbar.className = 'headers-toolbar';
  toolbar.innerHTML = `<button class="body-tool body-tool-save" data-action="save-headers">Save</button><button class="body-tool body-tool-cancel" data-action="cancel-headers">Cancel</button><button class="body-tool" data-action="add-header">+ Add</button>`;
  container.appendChild(toolbar);
}

function saveHeaders(id) {
  const container = document.querySelector('.headers-container[data-target="request"]');
  if (!container) return;

  const headers = {};
  container.querySelectorAll('.header-row').forEach(row => {
    const keyInput = row.querySelector('.header-key-input');
    const valInput = row.querySelector('.header-value-input');
    if (keyInput && valInput) {
      const key = keyInput.value.trim();
      if (key) {
        headers[key] = [valInput.value];
      }
    }
  });

  fetch(`/api/requests/${id}/headers`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ headers })
  }).then(r => r.json()).then(() => {
    const container = document.querySelector('.headers-container[data-target="request"]');
    if (!container) return;

    function buildViewRows(hdrs) {
      if (!hdrs || Object.keys(hdrs).length === 0) return '<div style="color:#666">No headers</div>';
      return Object.entries(hdrs).map(([k, v]) => {
        const val = Array.isArray(v) ? v.join(', ') : v;
        const dv = Array.isArray(v) ? JSON.stringify(v) : JSON.stringify([v]);
        return `<div class="header-row" data-key="${escapeHtml(k)}" data-values='${escapeHtml(dv)}'><span class="header-key">${escapeHtml(k)}:</span><span class="header-value">${escapeHtml(val)}</span></div>`;
      }).join('');
    }

    const editedHtml = buildViewRows(headers);
    const origHtml = container.dataset.originalHtml || container.dataset.editedHtml || '';

    container.innerHTML = editedHtml;
    container.dataset.editedHtml = editedHtml;
    container.dataset.originalHtml = origHtml;
    container.dataset.headerMode = 'edited';
    delete container.dataset.editing;
    delete container.dataset.original;

    const sectionPanel = container.closest('.section-panel');
    if (!sectionPanel) return;

    let toolbar = sectionPanel.querySelector('.content-toolbar');
    if (!toolbar) {
      toolbar = document.createElement('div');
      toolbar.className = 'content-toolbar';
      const contentBlock = sectionPanel.querySelector('.content-block');
      contentBlock.insertBefore(toolbar, contentBlock.firstChild);
    }
    toolbar.innerHTML = `
            <div class="toolbar-left">
                <div class="body-tools-group">
                    <button class="body-tool body-content" data-action="set-header-content" data-content="original">Original</button>
                    <button class="body-tool body-content active" data-action="set-header-content" data-content="edited">Edited</button>
                </div>
            </div>
            <div class="toolbar-right">
                <span class="body-badge body-badge-edited">edited</span>
            </div>`;
    const kebabMenu = sectionPanel.querySelector('.kebab-menu');
    if (kebabMenu && !kebabMenu.querySelector('[data-action="revert-headers"]')) {
      kebabMenu.insertAdjacentHTML('beforeend', '<div class="menu-item" data-action="revert-headers">↩ Revert</div>');
    }
  }).catch(e => console.error('Save headers failed:', e));
}

function cancelHeadersEdit(container) {
  if (!container) return;
  container.innerHTML = container.dataset.original;
  delete container.dataset.editing;
  delete container.dataset.original;
}

function addHeaderRow(container) {
  if (!container) return;
  const toolbar = container.querySelector('.headers-toolbar');
  const row = document.createElement('div');
  row.className = 'header-row';
  row.dataset.key = '';
  row.dataset.values = '[""]';
  row.innerHTML = `<input class="header-key-input" value="" placeholder="Key" /><span class="header-colon">:</span><input class="header-value-input" value="" placeholder="Value" /><button class="header-remove" data-action="remove-header" title="Remove">&times;</button>`;
  container.insertBefore(row, toolbar);
  row.querySelector('.header-key-input').focus();
}

function removeHeaderRow(btn) {
  const row = btn.closest('.inline-header-row') || btn.closest('.header-row');
  if (row) row.remove();
}

function addInlineHeaderRow(containerId) {
  const container = document.getElementById(containerId);
  if (!container) return;
  const row = document.createElement('div');
  row.className = 'inline-header-row';
  row.innerHTML = '<input class="inline-header-key" placeholder="Key"><span class="header-colon">:</span><input class="inline-header-value" placeholder="Value"><button class="header-remove" title="Remove">&times;</button>';
  container.appendChild(row);
  row.querySelector('.inline-header-key').focus();
}

function readInlineHeaders(containerId) {
  const container = document.getElementById(containerId);
  if (!container) return {};
  const headers = {};
  container.querySelectorAll('.inline-header-row').forEach(row => {
    const key = row.querySelector('.inline-header-key')?.value.trim();
    const val = row.querySelector('.inline-header-value')?.value;
    if (key) {
      if (headers[key]) {
        headers[key].push(val);
      } else {
        headers[key] = [val];
      }
    }
  });
  return headers;
}

document.getElementById('ruleModalClose').addEventListener('click', () => { disposeModalMonacoEditors(); closeRuleModal(); });
document.getElementById('ruleModalCancelBtn').addEventListener('click', () => { disposeModalMonacoEditors(); closeRuleModal(); });

document.getElementById('ruleModal').addEventListener('modal-opening', disposeModalMonacoEditors);
document.getElementById('ruleModal').addEventListener('click', (e) => {
  if (e.target.classList.contains('modal-overlay')) { disposeModalMonacoEditors(); closeRuleModal(); }
});

document.querySelectorAll('input[name="ruleRequestAction"]').forEach(radio => {
  radio.addEventListener('change', (e) => {
    const action = e.target.value;
    document.getElementById('modifyRequestSection').style.display = action === 'modify' ? '' : 'none';
    document.getElementById('mockRequestSection').style.display = action === 'mock' ? '' : 'none';
    const showResponse = action === 'passthrough' || action === 'modify';
    document.getElementById('responseSection').style.display = showResponse ? '' : 'none';
    if (!showResponse) {
      const realRadio = document.querySelector('input[name="ruleResponseAction"][value="real"]');
      if (realRadio) realRadio.checked = true;
    }

    if (action === 'modify') {
      const container = document.getElementById('modifyBodyEditor');
      initModalMonaco('modifyBodyEditor', container?.dataset.initialBody || '', 'modifyBody');
    }
    if (action === 'mock') {
      const container = document.getElementById('mockRequestBodyEditor');
      initModalMonaco('mockRequestBodyEditor', container?.dataset.initialBody || '', 'mockReqBody');
    }
  });
});

document.querySelectorAll('input[name="ruleResponseAction"]').forEach(radio => {
  radio.addEventListener('change', (e) => {
    document.getElementById('mockResponseSection').style.display = e.target.value === 'response_mock' ? '' : 'none';
    if (e.target.value === 'response_mock') {
      const container = document.getElementById('mockResponseBodyEditor');
      initModalMonaco('mockResponseBodyEditor', container?.dataset.initialBody || '', 'mockRespBody');
    }
  });
});

document.getElementById('addModifyHeader').addEventListener('click', () => addInlineHeaderRow('modifyHeaders'));
document.getElementById('addMockReqHeader').addEventListener('click', () => addInlineHeaderRow('mockRequestHeaders'));
document.getElementById('addMockRespHeader').addEventListener('click', () => addInlineHeaderRow('mockResponseHeaders'));

document.getElementById('modifyHeaders').addEventListener('click', (e) => {
  if (e.target.closest('.header-remove')) removeHeaderRow(e.target);
});
document.getElementById('mockRequestHeaders').addEventListener('click', (e) => {
  if (e.target.closest('.header-remove')) removeHeaderRow(e.target);
});
document.getElementById('mockResponseHeaders').addEventListener('click', (e) => {
  if (e.target.closest('.header-remove')) removeHeaderRow(e.target);
});

['modifyHeaders', 'mockRequestHeaders', 'mockResponseHeaders'].forEach(id => {
  document.getElementById(id).addEventListener('paste', (e) => {
    const target = e.target;
    if (!target.classList.contains('inline-header-key') && !target.classList.contains('inline-header-value')) return;
    const text = (e.clipboardData || window.clipboardData).getData('text');
    if (!text) return;
    const lines = text.split(/\r?\n/).filter(l => l.includes(':'));
    if (lines.length === 0) return;
    e.preventDefault();
    const container = document.getElementById(id);
    container.innerHTML = '';
    let lastValueInput = null;
    lines.forEach(line => {
      const idx = line.indexOf(':');
      const key = line.substring(0, idx).trim();
      const value = line.substring(idx + 1).trim();
      const row = document.createElement('div');
      row.className = 'inline-header-row';
      row.innerHTML = `<input class="inline-header-key" value="${escapeHtml(key)}"><span class="header-colon">:</span><input class="inline-header-value" value="${escapeHtml(value)}"><button class="header-remove" title="Remove">&times;</button>`;
      container.appendChild(row);
      lastValueInput = row.querySelector('.inline-header-value');
    });
    if (lastValueInput) lastValueInput.focus();
  });
});

document.getElementById('ruleModalSaveBtn').addEventListener('click', async () => {
  const modal = document.getElementById('ruleModal');
  const id = modal.dataset.ruleId;
  const name = document.getElementById('ruleName').value.trim();
  if (!name) { alert('Rule name is required'); return; }

  const method = document.getElementById('ruleMethod').value;
  const host = document.getElementById('ruleHost').value.trim();
  const urlPattern = document.getElementById('ruleUrl').value.trim();
  const reqAction = document.querySelector('input[name="ruleRequestAction"]:checked').value;
  const respAction = document.querySelector('input[name="ruleResponseAction"]:checked').value;

  const rule = {
    name,
    match: { method, host, url_pattern: urlPattern },
    action: reqAction,
    enabled: true,
  };

  if (reqAction === 'modify') {
    const modifyHost = document.getElementById('modifyHost').value.trim();
    if (!modifyHost) {
      alert('Host is required for Modify action');
      return;
    }
    rule.modified_request = {
      host: modifyHost,
      url: document.getElementById('modifyUrl').value.trim(),
      headers: readInlineHeaders('modifyHeaders'),
      body: modalMonacoEditors.modifyBody?.getValue() || '',
    };
  }

  if (reqAction === 'mock') {
    rule.mock_response = {
      status: parseInt(document.getElementById('mockRequestStatus').value) || 200,
      headers: readInlineHeaders('mockRequestHeaders'),
      body: modalMonacoEditors.mockReqBody?.getValue() || '',
    };
  }

  const canResponseMock = reqAction === 'passthrough' || reqAction === 'modify';
  if (canResponseMock && respAction === 'response_mock') {
    rule.action = 'response_mock';
    rule.mock_response = {
      status: parseInt(document.getElementById('mockResponseStatus').value) || 200,
      headers: readInlineHeaders('mockResponseHeaders'),
      body: modalMonacoEditors.mockRespBody?.getValue() || '',
    };
  }

  if (id) {
    rule.id = id;
    await updateRule(id, rule);
  } else {
    const result = await createRule(rule);
    if (result && result.deactivated && result.deactivated.length > 0) {
      setTimeout(() => alert(`Deactivated conflicting rules: ${result.deactivated.join(', ')}`), 100);
    }
  }
  disposeModalMonacoEditors();
  closeRuleModal();
});

let matchCheckTimeout = null;
function checkMatchDebounced() {
  clearTimeout(matchCheckTimeout);
  matchCheckTimeout = setTimeout(async () => {
    const modal = document.getElementById('ruleModal');
    if (!modal.classList.contains('open')) return;
    const method = document.getElementById('ruleMethod').value;
    const host = document.getElementById('ruleHost').value.trim();
    const urlPattern = document.getElementById('ruleUrl').value.trim();
    if (!method && !host && !urlPattern) {
      document.getElementById('matchWarning').style.display = 'none';
      return;
    }
    const excludeId = modal.dataset.ruleId || '';
    const matches = await checkMatch(method, host, urlPattern, excludeId);
    const warning = document.getElementById('matchWarning');
    if (matches.length > 0) {
      warning.style.display = '';
      warning.innerHTML = `A rule with this match already exists: <strong>${escapeHtml(matches[0].name)}</strong>. <a data-action="edit-matching-rule" data-rule-id="${matches[0].id}" style="color:#f0883e;cursor:pointer;text-decoration:underline">Edit it instead</a>`;
    } else {
      warning.style.display = 'none';
    }
  }, 300);
}

document.getElementById('ruleMethod').addEventListener('change', checkMatchDebounced);
document.getElementById('ruleHost').addEventListener('input', checkMatchDebounced);
document.getElementById('ruleUrl').addEventListener('input', checkMatchDebounced);

document.getElementById('ruleModal').addEventListener('click', (e) => {
  const link = e.target.closest('[data-action="edit-matching-rule"]');
  if (link) {
    const rule = rules.find(r => r.id === link.dataset.ruleId);
    if (rule) {
      disposeModalMonacoEditors();
      closeRuleModal();
      setTimeout(() => openRuleModal(rule), 100);
    }
  }
});

function toggleKebabMenu(kebab) {
  const menu = kebab.querySelector('.kebab-menu');
  if (!menu) return;
  const isOpen = menu.classList.contains('open');
  closeAllKebabMenus();
  if (!isOpen) menu.classList.add('open');
}

function closeAllKebabMenus() {
  document.querySelectorAll('.kebab-menu.open').forEach(m => m.classList.remove('open'));
}

document.addEventListener('click', (e) => {
  if (e.target.closest('.kebab, .kebab-menu')) return;
  closeAllKebabMenus();
});

// Filter system
const filterChips = document.getElementById('filterChips');
const filterOverflowPanel = document.getElementById('filterOverflowPanel');
const filterOverflowChips = document.getElementById('filterOverflowChips');
const overflowAddFilterBtn = document.getElementById('overflowAddFilterBtn');
const filterBarHeader = document.getElementById('filterBarHeader');
const filterModeToggle = document.getElementById('filterModeToggle');

function updateAgentBanner() {
  const banner = document.getElementById('agentBanner');
  if (!banner) return;
  banner.style.display = agentExposed ? 'block' : 'none';
}

function refreshFilters() {
  invalidateFilterCache();
  updateToggleUI();
  renderFilterChips();
  renderList();
  updateAgentBanner();
}

setOnFilterChange(refreshFilters);
setOnListRefresh(loadRequests);
setOnSelectedUpdated((id) => {
  if (id !== selectedId) return;
  fetch(`/api/requests/${id}`)
    .then(r => r.json())
    .then(entry => updateResponseInPlace(entry))
    .catch(e => console.error('Failed to refresh response detail:', e));
});
initBodyTypes({ refreshDetail, createMonacoEditor, mapContentType, renderCurrentContent });

function renderFilterChips() {
  const chips = getFilterChipsData();
  if (chips.length === 0) {
    filterChips.innerHTML = '';
    closeOverflowPanel();
    return;
  }

  closeOverflowPanel();

  if (chips.length > 1) {
    const chipCount = chips.filter(c => c.type !== 'connector').length;
    if (chipCount > 1) {
      const visible = chips.slice(0, 1).map(c => c.html).join('');
      filterChips.innerHTML = visible + `<span class="filter-chips-more" id="filterChipsMore">+${chipCount - 1} more</span>`;
      document.getElementById('filterChipsMore').addEventListener('click', toggleOverflowPanel);
    } else {
      filterChips.innerHTML = chips.map(c => c.html).join('');
    }
  } else {
    filterChips.innerHTML = chips[0].html;
  }
  renderOverflowChips();
}

function renderOverflowChips() {
  const chips = getFilterChipsData();
  filterOverflowChips.innerHTML = chips.map(c => c.html).join('');
}

function toggleOverflowPanel() {
  if (filterOverflowPanel.style.display === 'none') {
    openOverflowPanel();
  } else {
    closeOverflowPanel();
  }
}

function openOverflowPanel() {
  renderOverflowChips();
  filterOverflowPanel.style.display = 'flex';
}

function closeOverflowPanel() {
  filterOverflowPanel.style.display = 'none';
}

overflowAddFilterBtn.addEventListener('click', (e) => {
  e.stopPropagation();
  closeOverflowPanel();
  openFilterPopover();
});

document.addEventListener('click', (e) => {
  if (!e.target.closest('.filter-overflow-panel') && !e.target.closest('.filter-chips-more')) {
    closeOverflowPanel();
  }
});

filterChips.addEventListener('click', (e) => {
  const close = e.target.closest('.filter-chip-close');
  if (close) {
    closeChip(close.dataset.type);
    return;
  }
  const chip = e.target.closest('.filter-chip');
  if (chip) openChip(chip.dataset.type);
});

filterOverflowChips.addEventListener('click', (e) => {
  const close = e.target.closest('.filter-chip-close');
  if (close) {
    closeChip(close.dataset.type);
    return;
  }
  const chip = e.target.closest('.filter-chip');
  if (chip) {
    closeOverflowPanel();
    openChip(chip.dataset.type);
  }
});

// Popover init
const addFilterBtn = document.getElementById('addFilterBtn');
const filterPopover = document.getElementById('filterPopover');

initFilterPopover();

// Match mode toggle
function updateToggleUI() {
  const mode = getMatchMode();
  const bodySearching = isBodySearching();
  filterModeToggle.querySelectorAll('.filter-mode-btn').forEach(btn => {
    btn.classList.toggle('active', btn.dataset.mode === mode);
    btn.classList.toggle('disabled', bodySearching);
  });
}

filterModeToggle.addEventListener('click', (e) => {
  const btn = e.target.closest('.filter-mode-btn');
  if (!btn || isBodySearching()) return;
  setMatchMode(btn.dataset.mode);
});

updateToggleUI();

addFilterBtn.addEventListener('click', (e) => {
  e.stopPropagation();
  if (filterPopover.style.display !== 'none') {
    closeFilterPopover();
  } else {
    openFilterPopover();
  }
});

document.addEventListener('click', (e) => {
  if (!e.target.closest('.filter-add-btn') && !e.target.closest('.filter-popover') && !e.target.closest('.filter-chips') && !e.target.closest('.filter-overflow-panel')) {
    closeFilterPopover();
  }
});

document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') closeFilterPopover();

  if (e.repeat || !e.ctrlKey || e.shiftKey || e.altKey) return;
  if (e.target.closest && e.target.closest('.monaco-editor')) return;
  const k = e.key.toLowerCase();
  if (k === 'b') {
    e.preventDefault();
    document.getElementById('toggleListBtn')?.click();
  } else if (k === 'j') {
    if (!document.getElementById('replayChip')) return;
    e.preventDefault();
    replayChipClick();
  }
});

renderFilterChips();

// SSE for signature updates
let eventSource = null;
function connectSSE() {
  eventSource = new EventSource('/api/process/events');
  eventSource.onmessage = (e) => {
    try {
      const data = JSON.parse(e.data);
      if (data.filePath) {
        const signedEl = document.getElementById('originSigned');
        const pathEl = document.getElementById('originPath');
        if (signedEl && pathEl && pathEl.getAttribute('title') === data.filePath) {
          if (data.isSigned) {
            signedEl.innerHTML = `<span class="origin-status signed">✓ Signed by ${escapeHtml(data.signerName || 'Unknown')}</span>`;
          } else {
            signedEl.innerHTML = '<span class="origin-status unsigned">✗ Unsigned</span>';
          }
        }
      }
    } catch (err) { }
  };
  eventSource.onerror = () => {
    eventSource.close();
    setTimeout(connectSSE, 3000);
  };
}
