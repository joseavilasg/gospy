import { requests } from './state.js';

function escapeHtml(s) {
    return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

const filterTypes = [];
const filterState = {};
let activeType = null;
let modalSelection = [];
let matchMode = localStorage.getItem('gospy-match-mode') || 'all';

let onFilterChange = () => {};

export function setOnFilterChange(cb) { onFilterChange = cb; }

export function registerFilter(config) {
    filterTypes.push(config);
    filterState[config.type] = JSON.parse(localStorage.getItem(config.localStorageKey) || '[]');
}

export function getFilter(type) {
    return filterState[type] || [];
}

export function setFilter(type, values) {
    filterState[type] = values;
    const config = filterTypes.find(f => f.type === type);
    if (config) localStorage.setItem(config.localStorageKey, JSON.stringify(values));
}

export function isAnyFilterActive() {
    return filterTypes.some(f => filterState[f.type].length > 0);
}

export function getMatchMode() { return matchMode; }
export function setMatchMode(mode) {
    matchMode = mode;
    localStorage.setItem('gospy-match-mode', mode);
    onFilterChange();
}

export function clearAllFilters() {
    for (const config of filterTypes) setFilter(config.type, []);
    matchMode = 'all';
    localStorage.removeItem('gospy-match-mode');
    onFilterChange();
}

export function applyFilters(result) {
    const active = filterTypes.filter(f => filterState[f.type].length > 0);
    if (active.length === 0) return result;

    if (matchMode === 'all') {
        for (const config of active) {
            const values = filterState[config.type];
            result = result.filter(r => values.includes(config.extractValue(r)));
        }
        return result;
    }
    return result.filter(r => active.some(config =>
        filterState[config.type].includes(config.extractValue(r))
    ));
}

export function getFilterChipsData() {
    const chips = [];
    const connector = matchMode === 'all' ? 'AND' : 'OR';
    const activeIndices = [];
    for (let i = 0; i < filterTypes.length; i++) {
        if (filterState[filterTypes[i].type].length > 0) activeIndices.push(i);
    }
    for (let j = 0; j < activeIndices.length; j++) {
        const i = activeIndices[j];
        const config = filterTypes[i];
        const values = filterState[config.type];
        const names = values.slice(0, 2).join(', ');
        const extra = values.length > 2 ? ` +${values.length - 2}` : '';
        const extraHtml = extra ? `<span class="filter-chip-extra">${escapeHtml(extra)}</span>` : '';
        const closeSVG = `<svg width="16" height="16" viewBox="0 0 16 16" fill="none"><path d="M4 4L12 12M12 4L4 12" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>`;
        const html = `<span class="filter-chip grouped" data-type="${config.type}"><span class="filter-chip-label">${escapeHtml(config.label)}:</span> <span class="filter-chip-value">${escapeHtml(names)}</span>${extraHtml}<span class="filter-chip-close" data-type="${config.type}">${closeSVG}</span></span>`;
        chips.push({ type: config.type, html });
        if (j < activeIndices.length - 1) {
            chips.push({ type: 'connector', html: `<span class="filter-chip-connector">${connector}</span>` });
        }
    }
    return chips;
}

export function closeChip(type) {
    setFilter(type, []);
    if (!isAnyFilterActive()) {
        matchMode = 'all';
        localStorage.removeItem('gospy-match-mode');
    }
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
let modalFilterInput, modalFilterDropdown, filterAddBtn, filterMatchCount, filterClearBtn;

export function initFilterPopover() {
    filterPopover = document.getElementById('filterPopover');
    filterStep1 = filterPopover.querySelector('#filterStep1');
    filterStep2 = filterPopover.querySelector('#filterStep2');
    filterTypeSearch = filterPopover.querySelector('#filterTypeSearch');
    modalFilterInput = filterPopover.querySelector('#modalFilterInput');
    modalFilterDropdown = filterPopover.querySelector('#modalFilterDropdown');
    filterAddBtn = filterPopover.querySelector('#filterAddBtn');
    filterMatchCount = filterPopover.querySelector('#filterMatchCount');
    filterClearBtn = filterPopover.querySelector('#filterClearBtn');

    const list = filterStep1.querySelector('.filter-popover-list');
    list.innerHTML = filterTypes.map(f =>
        `<button class="filter-popover-item" data-type="${f.type}">${escapeHtml(f.label)} ›</button>`
    ).join('');

    filterStep1.addEventListener('click', (e) => {
        const item = e.target.closest('.filter-popover-item');
        if (!item || item.classList.contains('disabled')) return;
        const config = filterTypes.find(f => f.type === item.dataset.type);
        if (config) showStep2(config);
    });

    filterPopover.querySelector('#filterTypeBack').addEventListener('click', goBackToStep1);

    modalFilterInput.addEventListener('input', () => renderDropdown());

    modalFilterDropdown.addEventListener('click', (e) => {
        const option = e.target.closest('.process-filter-option');
        if (!option) return;
        const value = option.dataset.value;
        const idx = modalSelection.indexOf(value);
        if (idx >= 0) {
            modalSelection.splice(idx, 1);
        } else {
            modalSelection.push(value);
        }
        requestAnimationFrame(() => renderDropdown());
    });

    filterAddBtn.addEventListener('click', (e) => {
        e.stopPropagation();
        if (activeType) {
            setFilter(activeType.type, [...modalSelection]);
            onFilterChange();
        }
        goBackToStep1();
    });

    filterClearBtn.addEventListener('click', (e) => {
        e.stopPropagation();
        modalSelection = [];
        renderDropdown();
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
    modalFilterInput.value = '';
    modalFilterInput.placeholder = config.searchPlaceholder || 'Search...';
    modalSelection = [...(filterState[config.type] || [])];
    modalFilterInput.focus();
    renderDropdown();
}

function goBackToStep1() {
    activeType = null;
    filterStep2.style.display = 'none';
    filterStep1.style.display = '';
    filterTypeSearch.value = '';
    filterTypeSearch.focus();
    filterTypeSearch.dispatchEvent(new Event('input'));
}

function renderDropdown() {
    if (!activeType) return;
    const counts = {};
    requests.forEach(r => {
        const val = activeType.extractValue(r);
        if (val) counts[val] = (counts[val] || 0) + 1;
    });
    const keys = Object.keys(counts).sort();
    const q = (modalFilterInput.value || '').toLowerCase();
    const items = keys.filter(k => !q || k.toLowerCase().includes(q));

    modalFilterDropdown.innerHTML = items.map(v => {
        const selected = modalSelection.includes(v);
        return `<div class="process-filter-option${selected ? ' selected' : ''}" data-value="${escapeHtml(v)}"><span class="check">${selected ? '✓' : ''}</span><span>${escapeHtml(v)}</span><span class="count">${counts[v] || 0}</span></div>`;
    }).join('');

    const total = requests.length;
    let matching = total;
    if (modalSelection.length > 0) {
        matching = requests.filter(r => modalSelection.includes(activeType.extractValue(r))).length;
    }
    filterMatchCount.textContent = `${matching.toLocaleString()} requests`;
    filterClearBtn.style.display = modalSelection.length > 0 ? '' : 'none';
}
