import { requests, filterText, setFilterText, focusEnabled, setFocusEnabled, applyFullList } from './state.js';

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

let onFilterChange = () => {};
let onListRefresh = () => {};
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
        if (config.type === 'body') continue;
        payload.filters[config.type] = filterState[config.type] || [];
    }
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
        syncCriteriaFromServer(data.filters, data.focusEnabled);
    }).catch(() => {
        criteriaDirty = false;
    });
}

export function syncCriteriaFromServer(filters, focusEnabledVal) {
    if (!filters) return;
    if (criteriaDirty) return;
    matchMode = filters.matchMode || 'all';
    for (const config of filterTypes) {
        if (config.type === 'body') continue;
        filterState[config.type] = filters[config.type] || [];
    }
    setFilterText(filters.text || '');
    setFocusEnabled(focusEnabledVal);
    const input = document.getElementById('filterInput');
    if (input) input.value = filters.text || '';
    const cb = document.getElementById('focusEnabled');
    if (cb) cb.checked = !!focusEnabledVal;
    onFilterChange();
}

export function isAnyFilterActive() {
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
        if (e.key === 'Enter' && activeType && activeType.popoverType === 'search') {
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

function showStep2(config) {
    activeType = config;
    filterStep1.style.display = 'none';
    filterStep2.style.display = '';
    filterStep2Title.textContent = config.label;

    filterConfirmBtn.textContent = 'Apply';
    filterMatchCount.style.display = '';
    filterClearBtn.style.display = '';
    modalFilterContent.style.display = '';

    if (config.popoverType === 'search') {
        modalFilterContent.innerHTML = `<input class="body-search-input" placeholder="Search text in bodies..." value="${escapeHtml(bodySearchState.q || '')}" autofocus>`;
        filterMatchCount.style.display = 'none';
        filterClearBtn.style.display = 'none';
        filterConfirmBtn.textContent = 'Search';
        modalFilterContent.querySelector('input').focus();
        return;
    }

    modalFilterContent.innerHTML = `<input type="text" placeholder="${escapeHtml(config.searchPlaceholder || 'Search...')}" id="modalFilterInput" class="modal-filter-input">
        <div class="filter-option-list" id="modalFilterOptions"></div>`;
    modalFilterInput = modalFilterContent.querySelector('#modalFilterInput');
    modalSelection = [...(filterState[config.type] || [])];
    currentOptions = [];
    loadOptions(config.type);
    modalFilterInput.focus();
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

export function isBodySearching() { return bodySearchState.scanning; }

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
    type: 'body',
    label: 'Body contains',
    isAdvanced: true,
    popoverType: 'search',
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
        localStorage.setItem('gospy-body-filter', JSON.stringify([q]));
        onFilterChange();

        fetch('/api/requests/search', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ q }),
            signal: ac.signal,
        }).then(handleSearchStream).catch(() => {});
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
        localStorage.removeItem('gospy-body-filter');
        bodySearchState = { q: '', scanning: false, scanned: 0, total: 0, matchCount: 0, abortController: null };
        fetch('/api/filters/body', { method: 'DELETE' }).catch(() => {});
        onListRefresh();
    },
});

export function restoreBodyFilter() {
    const saved = JSON.parse(localStorage.getItem('gospy-body-filter') || '[]');
    if (saved.length > 0 && typeof saved[0] === 'string' && saved[0].length >= 3 && !bodySearchState.scanning) {
        const config = filterTypes.find(f => f.type === 'body');
        if (config) config.onSearch(saved[0]);
    }
}
