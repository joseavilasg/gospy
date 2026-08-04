import { requests, filterText, setFilterText, focusEnabled, setFocusEnabled, getAgentPreview, setAgentPreview, setAgentEnabled, setAgentExposed, applyFullList } from './state.js';

function escapeHtml(s) {
  return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

const closeSVG = `<svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M4 4L12 12M12 4L4 12" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>`;

const filterTypes = [];
const filterState = {};
let activeType = null;
let modalSelection = [];
let currentOptions = [];
let matchMode = 'all';

let onFilterChange = () => { };
let onListRefresh = () => { };
let criteriaSaveTimer = null;
let criteriaSaveSeq = 0;
let criteriaDirty = false;

export function setOnFilterChange(cb) { onFilterChange = cb; }
export function setOnListRefresh(cb) { onListRefresh = cb; }

export function registerFilter(config) {
  filterTypes.push(config);
  filterState[config.type] = [];
}

export function setFilter(type, values) {
  filterState[type] = values;
  if (type !== 'body') queueCriteriaSave();
}

export function queueCriteriaSave() {
  criteriaDirty = true;
  clearTimeout(criteriaSaveTimer);
  criteriaSaveTimer = setTimeout(saveCriteria, 300);
}

export function saveCriteria() {
  clearTimeout(criteriaSaveTimer);
  criteriaSaveTimer = null;
  const mySeq = ++criteriaSaveSeq;

  const payload = {
    filters: {},
    focusEnabled: focusEnabled,
  };
  for (const config of filterTypes) {
    if (config.type === 'body' || config.type === 'date') continue;
    payload.filters[config.type] = filterState[config.type] || [];
  }
  const dateRange = filterState.date || [];
  if (dateRange[0]) payload.filters.from = dateRange[0];
  if (dateRange[1]) payload.filters.to = dateRange[1];
  payload.filters.text = filterText;
  payload.filters.matchMode = matchMode;

  return fetch('/api/filters', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  }).then(r => r.json()).then(data => {
    if (mySeq !== criteriaSaveSeq) return;
    criteriaDirty = false;
    applyFullList(data);
    syncCriteriaFromServer(data.filters, data.focusEnabled, {
      preview: data.agentPreview,
      enabled: data.agentEnabled,
      exposed: data.agentExposed,
    });
  }).catch(() => {
    criteriaDirty = false;
  });
}

export function invalidateCriteriaSave() {
  clearTimeout(criteriaSaveTimer);
  criteriaSaveTimer = null;
  criteriaSaveSeq++;
  criteriaDirty = false;
}

export function syncCriteriaFromServer(filters, focusEnabledVal, agentState) {
  if (!filters) return;
  if (criteriaDirty) return;
  matchMode = filters.matchMode || 'all';
  for (const config of filterTypes) {
    if (config.type === 'body' || config.type === 'date') continue;
    filterState[config.type] = filters[config.type] || [];
  }
  filterState.date = [filters.from || '', filters.to || ''];
  setFilterText(filters.text || '');
  setFocusEnabled(focusEnabledVal);
  if (agentState) {
    setAgentPreview(agentState.preview);
    setAgentEnabled(agentState.enabled);
    setAgentExposed(agentState.exposed);
    const cb = document.getElementById('agentPreview');
    if (cb) cb.checked = !!agentState.preview;
    const cbGate = document.getElementById('agentEnabled');
    if (cbGate) cbGate.checked = !!agentState.enabled;
  }
  const input = document.getElementById('filterInput');
  if (input) input.value = filters.text || '';
  const cb2 = document.getElementById('focusEnabled');
  if (cb2) cb2.checked = !!focusEnabledVal;
  onFilterChange();
}

export function isAnyFilterActive() {
  if (filterText) return true;
  return filterTypes.some(f => f.getIsActive ? f.getIsActive() : (filterState[f.type] || []).length > 0);
}

export function getMatchMode() { return matchMode; }
export function setMatchMode(mode) {
  matchMode = mode;
  queueCriteriaSave();
}

export function getFilterChipsData() {
  const chips = [];
  const connector = matchMode === 'all' ? 'AND' : 'OR';
  const activeIndices = [];
  for (let i = 0; i < filterTypes.length; i++) {
    const ft = filterTypes[i];
    const isActive = ft.getIsActive ? ft.getIsActive() : filterState[ft.type].length > 0;
    if (isActive) activeIndices.push(i);
  }
  for (let j = 0; j < activeIndices.length; j++) {
    const i = activeIndices[j];
    const config = filterTypes[i];

    if (config.customChip) {
      const html = config.renderChip();
      if (!html) continue;
      chips.push({ type: config.type, html });
      if (j < activeIndices.length - 1) {
        chips.push({ type: 'connector', html: `<span class="filter-chip-connector">${connector}</span>` });
      }
      continue;
    }

    const values = filterState[config.type];
    const names = values.slice(0, 2).join(', ');
    const extra = values.length > 2 ? ` +${values.length - 2}` : '';
    const extraHtml = extra ? `<span class="filter-chip-extra">${escapeHtml(extra)}</span>` : '';
    const chipLabel = config.chipLabel || config.label;
    const html = `<span class="filter-chip grouped" data-type="${config.type}"><span class="filter-chip-label">${escapeHtml(chipLabel)}:</span> <span class="filter-chip-value">${escapeHtml(names)}</span>${extraHtml}<span class="filter-chip-close" data-type="${config.type}">${closeSVG}</span></span>`;
    chips.push({ type: config.type, html });
    if (j < activeIndices.length - 1) {
      chips.push({ type: 'connector', html: `<span class="filter-chip-connector">${connector}</span>` });
    }
  }
  return chips;
}

export function closeChip(type) {
  const config = filterTypes.find(f => f.type === type);
  if (config?.onClose) config.onClose();
  setFilter(type, []);
  onFilterChange();
}

export function openChip(type) {
  const config = filterTypes.find(f => f.type === type);
  if (config) {
    showStep2(config);
    const popover = document.getElementById('filterPopover');
    if (popover) popover.style.display = 'block';
  }
}

// --- Popover ---

let filterPopover, filterStep1, filterStep2, filterTypeSearch;
let modalFilterInput, modalFilterContent, filterStep2Title, filterConfirmBtn, filterMatchCount, filterClearBtn;

export function initFilterPopover() {
  filterPopover = document.getElementById('filterPopover');
  filterStep1 = filterPopover.querySelector('#filterStep1');
  filterStep2 = filterPopover.querySelector('#filterStep2');
  filterTypeSearch = filterPopover.querySelector('#filterTypeSearch');
  modalFilterContent = filterPopover.querySelector('#modalFilterContent');
  filterStep2Title = filterPopover.querySelector('#filterStep2Title');
  filterConfirmBtn = filterPopover.querySelector('#filterConfirmBtn');
  filterMatchCount = filterPopover.querySelector('#filterMatchCount');
  filterClearBtn = filterPopover.querySelector('#filterClearBtn');

  const list = filterStep1.querySelector('.filter-popover-content');
  const regular = filterTypes.filter(f => !f.isAdvanced);
  const advanced = filterTypes.filter(f => f.isAdvanced);
  list.innerHTML = regular.map(f =>
    `<button class="filter-popover-item" data-type="${f.type}">${escapeHtml(f.label)} ›</button>`
  ).join('');
  if (advanced.length > 0) {
    list.innerHTML += `<div class="filter-popover-separator">Advanced · may take time</div>`;
    list.innerHTML += advanced.map(f =>
      `<button class="filter-popover-item" data-type="${f.type}">${escapeHtml(f.label)} ›</button>`
    ).join('');
  }

  filterStep1.addEventListener('click', (e) => {
    const item = e.target.closest('.filter-popover-item');
    if (!item || item.classList.contains('disabled')) return;
    const config = filterTypes.find(f => f.type === item.dataset.type);
    if (config) showStep2(config);
  });

  filterPopover.querySelector('#filterTypeBack').addEventListener('click', goBackToStep1);

  modalFilterContent.addEventListener('input', (e) => {
    if (e.target.id === 'modalFilterInput') renderOptions();
  });

  modalFilterContent.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' && activeType?.onStep2Apply) {
      filterConfirmBtn.click();
    }
  });

  modalFilterContent.addEventListener('click', (e) => {
    const option = e.target.closest('.filter-option');
    if (!option) return;
    const value = option.dataset.value;
    const idx = modalSelection.indexOf(value);
    if (idx >= 0) {
      modalSelection.splice(idx, 1);
    } else {
      modalSelection.push(value);
    }
    requestAnimationFrame(() => renderOptions());
  });

  filterConfirmBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    if (activeType?.onStep2Apply) {
      activeType.onStep2Apply();
    } else if (activeType) {
      setFilter(activeType.type, [...modalSelection]);
    }
    onFilterChange();
    goBackToStep1();
  });

  filterClearBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    modalSelection = [];
    renderOptions();
  });

  filterTypeSearch.addEventListener('input', () => {
    const q = filterTypeSearch.value.toLowerCase();
    const items = filterStep1.querySelectorAll('.filter-popover-item');
    items.forEach(item => {
      item.style.display = (!q || item.textContent.toLowerCase().includes(q)) ? '' : 'none';
    });
  });
}

export function openFilterPopover() {
  filterPopover.style.display = 'block';
  filterStep1.style.display = '';
  filterStep2.style.display = 'none';
  filterTypeSearch.value = '';
  filterTypeSearch.focus();
}

export function closeFilterPopover() {
  filterPopover.style.display = 'none';
  filterStep1.style.display = '';
  filterStep2.style.display = 'none';
}

const STEP2_RENDERERS = {
  list(config) {
    modalFilterContent.innerHTML = `<input type="text" placeholder="${escapeHtml(config.searchPlaceholder || 'Search...')}" id="modalFilterInput" class="modal-filter-input">
            <div class="filter-option-list" id="modalFilterOptions"></div>`;
    modalFilterInput = modalFilterContent.querySelector('#modalFilterInput');
    modalSelection = [...(filterState[config.type] || [])];
    currentOptions = [];
    loadOptions(config.type);
    modalFilterInput.focus();
  },

  value(config) {
    const current = (filterState[config.type] || [])[0] || '';
    modalFilterContent.innerHTML = `<input type="${config.inputType || 'text'}" placeholder="${escapeHtml(config.searchPlaceholder || 'Type a value...')}" id="modalFilterInput" class="modal-filter-input" value="${escapeHtml(current)}" autofocus>`;
    modalFilterInput = modalFilterContent.querySelector('#modalFilterInput');
    modalFilterInput.focus();
  },

  range(config) {
    const current = filterState[config.type] || [];
    modalFilterContent.innerHTML = `<div class="filter-range-row">
                <label>From</label>
                <input type="datetime-local" id="modalFilterFrom" class="modal-filter-input" value="${escapeHtml(current[0] || '')}">
            </div>
            <div class="filter-range-row">
                <label>To</label>
                <input type="datetime-local" id="modalFilterTo" class="modal-filter-input" value="${escapeHtml(current[1] || '')}">
            </div>`;
    modalFilterContent.querySelector('#modalFilterFrom').focus();
  },

  search() {
    modalFilterContent.innerHTML = `<input class="body-search-input" placeholder="Search text in bodies..." value="${escapeHtml(bodySearchState.q || '')}" autofocus>`;
    modalFilterContent.querySelector('input').focus();
  },
};

function showStep2(config) {
  activeType = config;
  filterStep1.style.display = 'none';
  filterStep2.style.display = '';
  filterStep2Title.textContent = config.label;

  filterConfirmBtn.textContent = config.confirmLabel || 'Apply';
  const simple = !!config.onStep2Apply;
  filterMatchCount.style.display = simple ? 'none' : '';
  filterClearBtn.style.display = simple ? 'none' : '';
  modalFilterContent.style.display = '';

  if (config.renderStep2) {
    config.renderStep2();
    return;
  }
  const render = STEP2_RENDERERS[config.popoverType || 'list'];
  (render || STEP2_RENDERERS.list)(config);
}

function goBackToStep1() {
  activeType = null;
  filterConfirmBtn.textContent = 'Apply';
  filterMatchCount.style.display = '';
  filterClearBtn.style.display = '';
  modalFilterContent.style.display = '';
  modalFilterContent.innerHTML = '';
  filterStep2.style.display = 'none';
  filterStep1.style.display = '';
  filterTypeSearch.value = '';
  filterTypeSearch.focus();
  filterTypeSearch.dispatchEvent(new Event('input'));
}

function loadOptions(type) {
  return fetch('/api/filters/options?type=' + encodeURIComponent(type))
    .then(r => r.json())
    .then(d => {
      currentOptions = d.values || [];
      renderOptions();
    })
    .catch(() => {
      currentOptions = [];
      renderOptions();
    });
}

function renderOptions() {
  if (!activeType) return;
  const q = ((modalFilterInput && modalFilterInput.value) || '').toLowerCase();
  const items = currentOptions.filter(o => !q || o.value.toLowerCase().includes(q));

  const list = modalFilterContent.querySelector('#modalFilterOptions');
  list.innerHTML = items.map(o => {
    const selected = modalSelection.includes(o.value);
    return `<div class="filter-option${selected ? ' selected' : ''}" data-value="${escapeHtml(o.value)}"><span class="check">${selected ? '✓' : ''}</span><span>${escapeHtml(o.value)}</span><span class="count">${o.count || 0}</span></div>`;
  }).join('');

  let matching = 0;
  for (const o of currentOptions) {
    if (modalSelection.includes(o.value)) matching += o.count;
  }
  filterMatchCount.textContent = `${matching.toLocaleString()} requests`;
  filterClearBtn.style.display = modalSelection.length > 0 ? '' : 'none';
}

// --- Body search ---

let bodySearchState = { q: '', scanning: false, scanned: 0, total: 0, matchCount: 0, abortController: null };

function bodyFilterKey() {
  return getAgentPreview() ? 'gospy-body-filter.agent' : 'gospy-body-filter';
}

export function isBodySearching() { return bodySearchState.scanning; }

export function resetBodySearchState() {
  bodySearchState = { q: '', scanning: false, scanned: 0, total: 0, matchCount: 0, abortController: null };
}

export function cancelBodySearch() {
  if (bodySearchState.abortController) bodySearchState.abortController.abort();
  resetBodySearchState();
}

async function handleSearchStream(resp) {
  const reader = resp.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  let flushTimer = null;
  let needsFlush = false;

  const flush = () => {
    if (!needsFlush) return;
    needsFlush = false;
    onFilterChange();
    onListRefresh();
  };

  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;

      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split('\n');
      buffer = lines.pop();

      for (const line of lines) {
        if (!line.trim()) continue;
        try {
          const msg = JSON.parse(line);
          if (msg.done !== undefined) {
            bodySearchState.scanning = false;
            bodySearchState.matchCount = msg.matchCount;
          } else if (msg.scanned !== undefined) {
            bodySearchState.scanned = msg.scanned;
            bodySearchState.total = msg.total;
            needsFlush = true;
          }
        } catch (_) { /* skip malformed lines */ }
      }

      if (needsFlush && !flushTimer) {
        flushTimer = setTimeout(() => {
          flushTimer = null;
          flush();
          if (needsFlush) flushTimer = setTimeout(flush, 150);
        }, 150);
      }
    }
  } catch (_) { /* connection errors / abort ignored */ }

  clearTimeout(flushTimer);
  bodySearchState.scanning = false;
  onFilterChange();
  onListRefresh();
}

registerFilter({
  type: 'process',
  label: 'Process',
  searchPlaceholder: 'Search processes...',
});

registerFilter({
  type: 'referer',
  label: 'Referer',
  searchPlaceholder: 'Search referers...',
});

registerFilter({
  type: 'host',
  label: 'Host',
  searchPlaceholder: 'Search hosts...',
});

registerFilter({
  type: 'requestContentType',
  label: 'Request Content-Type',
  chipLabel: 'Req CT',
  searchPlaceholder: 'Search request content types...',
});

registerFilter({
  type: 'responseContentType',
  label: 'Response Content-Type',
  chipLabel: 'Resp CT',
  searchPlaceholder: 'Search response content types...',
});

registerFilter({
  type: 'origin',
  label: 'Origin',
  searchPlaceholder: 'Search origins...',
});

registerFilter({
  type: 'method',
  label: 'Method',
  searchPlaceholder: 'Search methods...',
});

registerFilter({
  type: 'body',
  label: 'Body contains',
  isAdvanced: true,
  popoverType: 'search',
  confirmLabel: 'Search',
  customChip: true,

  getIsActive() {
    return bodySearchState.scanning || bodySearchState.q !== '';
  },

  renderChip() {
    const s = bodySearchState;
    if (s.scanning) {
      return `<span class="filter-chip body-chip grouped searching" data-type="body">
                <span class="spinner"></span>
                <span class="filter-chip-label">Body:</span>
                <span class="filter-chip-value">"${escapeHtml(s.q)}" — ${s.scanned}/${s.total}</span>
                <span class="filter-chip-close" data-type="body">${closeSVG}</span>
            </span>`;
    }
    if (!s.q) return '';
    const count = s.matchCount;
    const cls = count === 0 ? 'zero' : 'completed';
    return `<span class="filter-chip body-chip grouped ${cls}" data-type="body">
            <span class="filter-chip-label">Body:</span>
            <span class="filter-chip-value">"${escapeHtml(s.q)}" (${count} match${count !== 1 ? 'es' : ''})</span>
            <span class="filter-chip-close" data-type="body">${closeSVG}</span>
        </span>`;
  },

  onSearch(q) {
    if (bodySearchState.abortController) bodySearchState.abortController.abort();
    const ac = new AbortController();
    bodySearchState = { q, scanning: true, scanned: 0, total: 0, matchCount: 0, abortController: ac };
    setFilter('body', []);
    localStorage.setItem(bodyFilterKey(), JSON.stringify([q]));
    onFilterChange();

    fetch('/api/requests/search', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ q }),
      signal: ac.signal,
    }).then(handleSearchStream).catch(() => { });
  },

  onStep2Apply() {
    const input = modalFilterContent.querySelector('input');
    const trimmed = (input.value || '').trim();
    if (trimmed && trimmed.length >= 3) {
      this.onSearch(trimmed);
    }
  },

  onClose() {
    if (bodySearchState.abortController) bodySearchState.abortController.abort();
    localStorage.removeItem(bodyFilterKey());
    resetBodySearchState();
    fetch('/api/filters/body', { method: 'DELETE' }).catch(() => { });
    onListRefresh();
  },
});

registerFilter({
  type: 'path',
  label: 'Path',
  popoverType: 'value',
  searchPlaceholder: 'Path substring, e.g. /api/issues/',

  onStep2Apply() {
    const trimmed = (modalFilterInput.value || '').trim();
    setFilter('path', trimmed ? [trimmed] : []);
  },
});

registerFilter({
  type: 'status',
  label: 'Status',
  popoverType: 'value',
  inputType: 'number',
  searchPlaceholder: 'HTTP status, e.g. 200',

  onStep2Apply() {
    const raw = (modalFilterInput.value || '').trim();
    if (raw && !/^\d+$/.test(raw)) {
      modalFilterInput.style.borderColor = 'var(--error)';
      return;
    }
    modalFilterInput.style.borderColor = '';
    setFilter('status', raw ? [raw] : []);
  },
});

registerFilter({
  type: 'date',
  label: 'Date range',
  popoverType: 'range',
  customChip: true,

  getIsActive() {
    return (filterState.date || []).some(v => !!v);
  },

  renderChip() {
    const [from, to] = filterState.date || [];
    if (!from && !to) return '';
    const fmt = v => {
      if (!v) return '∞';
      const d = new Date(v);
      return isNaN(d) ? v : d.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
    };
    return `<span class="filter-chip grouped" data-type="date">
            <span class="filter-chip-label">Time:</span>
            <span class="filter-chip-value">${escapeHtml(fmt(from))} → ${escapeHtml(fmt(to))}</span>
            <span class="filter-chip-close" data-type="date">${closeSVG}</span>
        </span>`;
  },

  onStep2Apply() {
    const from = (modalFilterContent.querySelector('#modalFilterFrom').value || '').trim();
    const to = (modalFilterContent.querySelector('#modalFilterTo').value || '').trim();
    setFilter('date', from || to ? [from, to] : []);
  },
});


export function restoreBodyFilter() {
  const saved = JSON.parse(localStorage.getItem(bodyFilterKey()) || '[]');
  if (saved.length > 0 && typeof saved[0] === 'string' && saved[0].length >= 3 && !bodySearchState.scanning) {
    const config = filterTypes.find(f => f.type === 'body');
    if (config) config.onSearch(saved[0]);
  }
}
