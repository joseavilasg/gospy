import { requests, selectedId, filterText, ignoredHosts, focusedHosts, focusEnabled, setSelectedId, rules, processFilter } from './state.js';

const ITEM_HEIGHT = 35;
const BUFFER = 5;
let lastFiltered = [];
let lastRange = { start: -1, end: -1 };
let filteredCache = null;

export const SVG_EDIT = '<svg width="14" height="14" viewBox="0 0 16 16"><path d="M11.5 1.5l3 3L5 14H2v-3L11.5 1.5z" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/></svg>';
export const SVG_REVERT = '<svg width="14" height="14" viewBox="0 0 16 16"><path d="M3 7h7a3 3 0 010 6H8" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/><polyline points="6,4 3,7 6,10" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>';

export function escapeHtml(str) {
    if (!str) return '';
    return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function hostMatchesPattern(host, pattern) {
    if (pattern === host) return true;
    if (pattern.includes('*')) {
        const regex = '^' + pattern.replace(/[.+?^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '.*') + '$';
        return new RegExp(regex).test(host);
    }
    return false;
}

function hostMatchesFocus(host) {
    if (!focusEnabled || focusedHosts.length === 0) return true;
    return focusedHosts.some(p => hostMatchesPattern(host, p));
}

function hostMatchesIgnore(host) {
    return ignoredHosts.some(p => hostMatchesPattern(host, p));
}

export function getFilteredRequests() {
    if (filteredCache) return filteredCache;
    let result = requests.filter(r => !hostMatchesIgnore(r.host));
    result = result.filter(r => hostMatchesFocus(r.host));

    if (processFilter.length > 0) {
        result = result.filter(r => processFilter.includes(r.clientDisplayName || r.clientProcess || ''));
    }

    if (filterText) {
        const q = filterText.toLowerCase();
        result = result.filter(r => {
            const method = (r.method || '').toLowerCase();
            const url = (r.url || r.host || '').toLowerCase();
            const status = r.status != null ? String(r.status) : '';
            const process = (r.clientDisplayName || r.clientProcess || '').toLowerCase();
            return method.includes(q) || url.includes(q) || status.includes(q) || process.includes(q);
        });
    }

    filteredCache = result;
    return result;
}

export function invalidateFilterCache() {
    filteredCache = null;
}

function buildItemHtml(r) {
    const method = r.method;
    const url = r.url || r.host;
    const status = r.status ?? null;
    const time = new Date(r.timestamp).toLocaleTimeString();
    const selected = r.id === selectedId ? ' selected' : '';
    const statusClass = status ? (status < 300 ? 'status-2xx' : status < 400 ? 'status-3xx' : status < 500 ? 'status-4xx' : 'status-5xx') : '';
    const replayBadge = r.replayedFrom
        ? '<span class="replay-badge" title="Replayed request">↻</span>'
        : '';

    let actionBadge = '';
    if (r.appliedAction === 'mock' || r.appliedAction === 'response_mock') {
        actionBadge = '<span class="action-badge action-badge-mock" title="Mocked by rule">◉</span>';
    } else if (r.appliedAction === 'drop') {
        actionBadge = '<span class="action-badge action-badge-drop" title="Dropped by rule">✕</span>';
    } else if (r.appliedAction === 'modify') {
        actionBadge = '<span class="action-badge action-badge-modify" title="Modified by rule">✎</span>';
    }

    let processBadge = '';
    if (r.clientProcess) {
        const badgeText = r.clientDisplayName || r.clientProcess;
        processBadge = `<span class="process-badge" title="${escapeHtml(r.clientProcess)}">${escapeHtml(badgeText)}</span>`;
    }

    return `<div class="request-item${selected}" title="${escapeHtml(url)}" data-id="${r.id}"><span class="method method-${method}">${method}</span><span class="url">${escapeHtml(url)}</span>${status != null ? `<span class="status ${statusClass}">${status}</span>` : ''}${actionBadge}${replayBadge}${processBadge}<span class="time">${time}</span></div>`;
}

export function renderList() {
    const list = document.getElementById('requestList');
    const filtered = getFilteredRequests();
    lastFiltered = filtered;
    const total = requests.length;

    if (filterText || processFilter.length > 0 || (focusEnabled && focusedHosts.length > 0)) {
        document.getElementById('stats').textContent = filtered.length + ' / ' + total + ' requests';
    } else {
        document.getElementById('stats').textContent = total + ' requests';
    }

    if (requests.length === 0) {
        list.innerHTML = '<div style="padding:20px;color:#666;text-align:center">Waiting for requests...</div>';
        lastRange = { start: -1, end: -1 };
        return;
    }

    lastRange = { start: -1, end: -1 };
    if (filtered.length === 0) {
        list.innerHTML = '<div style="padding:20px;color:#666;text-align:center">No matching requests</div>';
        return;
    }
    renderVisibleItems(list, filtered);
}

function renderVisibleItems(list, filtered) {
    if (!filtered) filtered = lastFiltered;
    if (!filtered || filtered.length === 0) return;

    const totalHeight = filtered.length * ITEM_HEIGHT;
    const scrollTop = list.scrollTop;
    const viewportHeight = list.clientHeight || 600;
    const start = Math.max(0, Math.floor(scrollTop / ITEM_HEIGHT) - BUFFER);
    const end = Math.min(filtered.length, Math.ceil((scrollTop + viewportHeight) / ITEM_HEIGHT) + BUFFER);

    if (start === lastRange.start && end === lastRange.end) return;
    lastRange = { start, end };

    const scrollTopSave = list.scrollTop;
    const visibleItems = filtered.slice(start, end);

    let html = `<div style="height:${totalHeight}px;position:relative">`;
    if (start > 0) html += `<div style="height:${start * ITEM_HEIGHT}px"></div>`;
    for (let i = 0; i < visibleItems.length; i++) {
        html += buildItemHtml(visibleItems[i]);
    }
    if (end < filtered.length) html += `<div style="height:${(filtered.length - end) * ITEM_HEIGHT}px"></div>`;
    html += '</div>';

    list.innerHTML = html;
    list.scrollTop = scrollTopSave;
}

export function onListScroll() {
    const list = document.getElementById('requestList');
    renderVisibleItems(list, lastFiltered);
}

export function selectRequest(id) {
    const oldEl = document.querySelector('.request-item.selected');
    if (oldEl) oldEl.classList.remove('selected');

    setSelectedId(id);

    const newEl = document.querySelector(`[data-id="${id}"]`);
    if (newEl) {
        newEl.classList.add('selected');
        newEl.scrollIntoView({ block: 'nearest' });
    }

    fetch(`/api/requests/${id}`)
        .then(resp => resp.json())
        .then(entry => renderDetail(entry))
        .catch(e => console.error('Failed to load request detail:', e));
}

export function renderDetail(req) {
    const panel = document.getElementById('detailPanel');
    const host = req.request.host || '';
    const isIgnored = ignoredHosts.includes(host);
    const isFocused = focusedHosts.includes(host);

    const reqOriginalHeaders = req.request.headers || {};
    const reqEditedHeaders = req.request.editedHeaders;
    const reqHasEditedHeaders = reqEditedHeaders && Object.keys(reqEditedHeaders).length > 0;
    const isModified = req.appliedAction === 'modify';
    const serverReqHeaders = req.serverRequest ? (req.serverRequest.headers || {}) : {};
    const hasServerReqHeaders = isModified && Object.keys(serverReqHeaders).length > 0;

    function buildHeaderRows(headers) {
        return Object.entries(headers).length > 0
            ? Object.entries(headers).map(([k,v]) => {
                const val = Array.isArray(v) ? v.join(', ') : v;
                const dataValues = Array.isArray(v) ? JSON.stringify(v) : JSON.stringify([v]);
                return `<div class="header-row" data-key="${escapeHtml(k)}" data-values='${escapeHtml(dataValues)}'><span class="header-key">${escapeHtml(k)}:</span><span class="header-value">${escapeHtml(val)}</span></div>`;
            }).join('')
            : '<div style="color:#666">No headers</div>';
    }

    const reqOriginalHtml = buildHeaderRows(reqOriginalHeaders);
    const reqEditedHtml = reqHasEditedHeaders ? buildHeaderRows(reqEditedHeaders) : '';
    const reqModifiedHtml = hasServerReqHeaders ? buildHeaderRows(serverReqHeaders) : '';
    const reqHeadersHtml = reqHasEditedHeaders ? reqEditedHtml : (isModified && hasServerReqHeaders ? reqModifiedHtml : reqOriginalHtml);

    const respHeaders = req.response && req.response.headers ? Object.entries(req.response.headers).map(([k,v]) =>
        `<div class="header-row"><span class="header-key">${escapeHtml(k)}:</span><span class="header-value">${escapeHtml(Array.isArray(v) ? v.join(', ') : v)}</span></div>`
    ).join('') : '<div style="color:#666">No response yet</div>';

    const reqBody = req.request.body || '';
    const respBody = req.response ? (req.response.body || '') : '';
    const reqRawBody = req.request.rawBody || '';
    const respRawBody = req.response ? (req.response.rawBody || '') : '';
    const reqCompression = req.request.compression || '';
    const respCompression = req.response ? (req.response.compression || '') : '';

    const reqContentType = req.request.headers?.['content-type']?.[0] || req.request.headers?.['Content-Type']?.[0] || '';
    const respContentType = req.response?.headers?.['content-type']?.[0] || req.response?.headers?.['Content-Type']?.[0] || '';

    const ignoreBtn = isIgnored
        ? `<button class="btn-active" data-action="unignore" data-host="${escapeHtml(host)}"><svg width="16" height="16" viewBox="0 0 16 16"><polyline points="3,8 7,12 13,4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg> Remove ignore</button>`
        : `<button class="btn-ignore-detail" data-action="ignore" data-host="${escapeHtml(host)}"><svg width="16" height="16" viewBox="0 0 16 16"><circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" stroke-width="2"/><line x1="5" y1="5" x2="11" y2="11" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg> Ignore host</button>`;

    let focusBtn;
    if (isFocused) {
        focusBtn = `<button class="btn-active btn-focus-active" data-action="unfocus" data-host="${escapeHtml(host)}"><svg width="16" height="16" viewBox="0 0 16 16"><polyline points="3,8 7,12 13,4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg> Focused</button>`;
    } else {
        focusBtn = `<button class="btn-focus-detail" data-action="focus" data-host="${escapeHtml(host)}"><svg width="16" height="16" viewBox="0 0 16 16"><circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" stroke-width="2"/><circle cx="8" cy="8" r="3" fill="currentColor"/></svg> Add to focus</button>`;
    }

    const SVG_COPY_SMALL = '<svg width="10" height="10" viewBox="0 0 16 16"><rect x="5" y="5" width="9" height="9" rx="1" fill="none" stroke="currentColor" stroke-width="1.5"/><path d="M5 11H3.5A1.5 1.5 0 012 9.5v-7A1.5 1.5 0 013.5 1h7A1.5 1.5 0 0112 2.5V5" fill="none" stroke="currentColor" stroke-width="1.5"/></svg>';
    function buildBodyViewer(target, body, rawBody, compression, hasEdited, editedBody, contentType, isModified, modifiedBody, modifiedContentType, isMocked, mockedBody, mockedContentType, canEdit) {
        const badges = [];
        if (compression) badges.push(`<span class="body-badge body-badge-compression">${escapeHtml(compression)}</span>`);
        if (hasEdited) badges.push(`<span class="body-badge body-badge-edited">edited</span>`);
        if (isModified) badges.push(`<span class="body-badge body-badge-modified">modified</span>`);
        if (isMocked) badges.push(`<span class="body-badge body-badge-mocked">mocked</span>`);
        const badgesHtml = badges.join('');

        const viewModeHtml = `<button class="body-tool body-view active" data-action="set-view" data-target="${target}" data-view="pretty">Pretty</button><button class="body-tool body-view" data-action="set-view" data-target="${target}" data-view="raw">Raw</button>`;

        const hasOtherContent = hasEdited || (isModified && modifiedBody) || (isMocked && mockedBody);
        const defaultContent = (isMocked && mockedBody) ? 'mocked' : 'original';
        const contentBtns = [];
        if (hasOtherContent) contentBtns.push(`<button class="body-tool body-content${defaultContent === 'original' ? ' active' : ''}" data-action="set-content" data-target="${target}" data-content="original">Original</button>`);
        if (hasEdited) contentBtns.push(`<button class="body-tool body-content" data-action="set-content" data-target="${target}" data-content="edited">Edited</button>`);
        if (isModified && modifiedBody) contentBtns.push(`<button class="body-tool body-content" data-action="set-content" data-target="${target}" data-content="modified">Modified</button>`);
        if (isMocked && mockedBody) contentBtns.push(`<button class="body-tool body-content${defaultContent === 'mocked' ? ' active' : ''}" data-action="set-content" data-target="${target}" data-content="mocked">Mocked</button>`);

        const menuItems = [`<div class="menu-item" data-action="copy-body" data-target="${target}">⧉ Copy</div>`];
        if (canEdit) menuItems.push(`<div class="menu-item" data-action="edit-body" data-target="${target}">✎ Edit</div>`);
        if (hasEdited) menuItems.push(`<div class="menu-item" data-action="revert-body" data-target="${target}">↩ Revert</div>`);

        const hasToolbar = badges.length > 0 || viewModeHtml.length > 0 || contentBtns.length > 0;
        const displayBody = body;

        return `<div class="section-panel" data-body-target="${target}" data-content-type="${escapeHtml(contentType)}">
            <div class="section-header">
                <span class="section-title">Body</span>
                <div class="kebab" data-action="toggle-menu">
                    ⋮
                    <div class="kebab-menu">${menuItems.join('')}</div>
                </div>
            </div>
            <div class="content-block">
                <div class="content-toolbar${hasToolbar ? '' : ' empty'}">
                    <div class="toolbar-left">
                        ${viewModeHtml ? `<div class="body-tools-group">${viewModeHtml}</div>` : ''}
                        ${contentBtns.length > 0 ? `<div class="divider-v"></div><div class="body-tools-group">${contentBtns.join('')}</div>` : ''}
                    </div>
                    <div class="toolbar-right">${badgesHtml}</div>
                </div>
                <pre class="body-content" data-body-target="${target}" data-decoded="${escapeHtml((isMocked && mockedBody) ? mockedBody : body)}" data-raw="${escapeHtml(rawBody)}" data-edited="${escapeHtml(hasEdited ? editedBody : '')}" data-modified="${escapeHtml(isModified ? modifiedBody : '')}" data-mocked="${escapeHtml(isMocked ? body : '')}" data-compression="${compression}" data-view-mode="pretty" data-content-mode="${defaultContent}">${escapeHtml(displayBody)}</pre>
            </div>
        </div>`;
    }

    const actionBanner = (() => {
        if (!req.appliedAction || req.appliedAction === 'passthrough') return '';
        const ruleLabel = req.ruleName ? ` by "${escapeHtml(req.ruleName)}"` : '';
        switch (req.appliedAction) {
            case 'mock': return `<div class="action-banner action-banner-mock">◉ Mocked${ruleLabel}</div>`;
            case 'drop': return `<div class="action-banner action-banner-drop">✕ Dropped${ruleLabel}</div>`;
            case 'modify': return `<div class="action-banner action-banner-modify">✎ Modified${ruleLabel}</div>`;
            case 'response_mock': return `<div class="action-banner action-banner-response-mock">↻ Response Mocked${ruleLabel}</div>`;
            default: return '';
        }
    })();

    const isMocked = req.appliedAction === 'mock' || req.appliedAction === 'response_mock';
    const isDropped = req.appliedAction === 'drop';

    const serverReqBody = req.serverRequest ? (req.serverRequest.body || '') : '';
    const serverRespBody = req.serverResponse ? (req.serverResponse.body || '') : '';
    const serverRespHeaders = req.serverResponse ? (req.serverResponse.headers || {}) : {};
    const serverReqContentType = req.serverRequest?.headers?.['content-type']?.[0] || req.serverRequest?.headers?.['Content-Type']?.[0] || '';
    const serverRespContentType = req.serverResponse?.headers?.['content-type']?.[0] || req.serverResponse?.headers?.['Content-Type']?.[0] || '';

    const canEdit = !isModified && !isMocked && !isDropped;

    let reqBodyHtml = '';
    if (reqBody) {
        const reqHasEdited = req.request.editedBody && req.request.editedBody !== '';
        reqBodyHtml = buildBodyViewer('request', reqBody, reqRawBody, reqCompression, reqHasEdited, req.request.editedBody, reqContentType, isModified, serverReqBody, serverReqContentType, false, '', '', canEdit);
    }

    let respBodyHtml = '';
    if (respBody) {
        const respHasEdited = req.response && req.response.editedBody && req.response.editedBody !== '';
        respBodyHtml = buildBodyViewer('response', respBody, respRawBody, respCompression, respHasEdited && canEdit, req.response.editedBody, respContentType, false, '', '', isMocked, serverRespBody, serverRespContentType, canEdit);
    }

    let replayedFromHtml = '';
    if (req.replayedFrom) {
        const origEntry = requests.find(r => r.id === req.replayedFrom);
        if (origEntry) {
            replayedFromHtml = `<div class="replayed-from"><span class="replayed-from-icon">↻</span> Replayed from: <a data-action="goto-replay" data-id="${req.replayedFrom}">${escapeHtml(origEntry.method)} ${escapeHtml(origEntry.url)}</a> · ${new Date(origEntry.timestamp).toLocaleTimeString()}</div>`;
        } else {
            replayedFromHtml = `<div class="replayed-from"><span class="replayed-from-icon">↻</span> Replayed from: <a data-action="goto-replay" data-id="${req.replayedFrom}">${req.replayedFrom.slice(0, 8)}</a></div>`;
        }
    }

    const replays = requests.filter(r => r.replayedFrom === req.id);
    let replaysHtml = '';
    if (replays.length > 0) {
        replaysHtml = `
        <div class="replays-section">
            <div class="replays-header" data-action="toggle-replays">Replays (${replays.length}) <span class="replays-toggle">▾</span></div>
            <div class="replays-list">
                ${replays.map(r => {
                    const rStatus = r.status != null ? r.status : '';
                    const rStatusClass = rStatus ? (rStatus < 300 ? 'status-2xx' : rStatus < 400 ? 'status-3xx' : rStatus < 500 ? 'status-4xx' : 'status-5xx') : '';
                    return `<div class="replay-item" data-action="goto-replay" data-id="${r.id}"><span class="method method-${r.method}">${r.method}</span><span class="url">${escapeHtml(r.url)}</span>${rStatus ? `<span class="status ${rStatusClass}">${rStatus}</span>` : ''}<span class="time">${new Date(r.timestamp).toLocaleTimeString()}</span></div>`;
                }).join('')}
            </div>
        </div>`;
    }

    panel.innerHTML = `
        ${actionBanner}
        <div class="detail-toolbar">
            ${ignoreBtn}
            ${focusBtn}
            <button class="btn-replay" data-action="send-replay"><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/></svg> Replay</button>
            <button class="btn-create-rule" data-action="create-rule-from-request"><svg width="16" height="16" viewBox="0 0 16 16" fill="none"><rect x="3" y="1" width="10" height="14" rx="1.5" stroke="currentColor" stroke-width="1.5"/><line x1="5.5" y1="5" x2="10.5" y2="5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/><line x1="5.5" y1="8" x2="10.5" y2="8" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/><line x1="8" y1="10.5" x2="8" y2="13" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/><line x1="6.5" y1="11.75" x2="9.5" y2="11.75" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg> Rule</button>
        </div>
        ${replayedFromHtml}
        <div class="tabs-row">
            <div class="tabs">
                <button class="tab active" data-action="tab" data-tab="request">Request</button>
                <button class="tab" data-action="tab" data-tab="response">Response</button>
                <button class="tab" data-action="tab" data-tab="origin">Origin</button>
            </div>
            <div class="detail-id-group">
                <span class="detail-id">${escapeHtml(req.id)}</span>
                <button class="detail-id-copy" data-action="copy-id" title="Copy ID">${SVG_COPY_SMALL}</button>
            </div>
        </div>

        <div id="tab-request" class="tab-content">
            ${isModified && req.serverRequest ? `
            <div class="section-panel">
                <div class="section-header">
                    <span class="section-title">Request</span>
                </div>
                <div class="content-block">
                    <div class="content-toolbar">
                        <div class="toolbar-left">
                            <div class="body-tools-group">
                                <button class="body-tool body-content" data-action="set-url-content" data-content="original">Original</button>
                                <button class="body-tool body-content active" data-action="set-url-content" data-content="modified">Modified</button>
                            </div>
                        </div>
                    </div>
                    <pre data-url-original="${escapeHtml(req.request.url || req.request.host)}" data-url-modified="${escapeHtml(req.serverRequest ? (req.serverRequest.url || req.serverRequest.host) : '')}">${escapeHtml(req.request.method)} ${isModified && req.serverRequest ? escapeHtml(req.serverRequest.url || req.serverRequest.host) : escapeHtml(req.request.url || req.request.host)}</pre>
                </div>
            </div>` : `
            <div class="section-panel">
                <div class="section-header">
                    <span class="section-title">Request</span>
                </div>
                <div class="content-block">
                    <pre data-url-original="${escapeHtml(req.request.url || req.request.host)}" data-url-modified="">${escapeHtml(req.request.method)} ${escapeHtml(req.request.url || req.request.host)}</pre>
                </div>
            </div>`}

            <div class="section-panel">
                <div class="section-header">
                    <span class="section-title">Headers</span>
                    <div class="kebab" data-action="toggle-menu">
                        ⋮
                        <div class="kebab-menu">
                            <div class="menu-item" data-action="copy-headers" data-target="request">⧉ Copy</div>
                            ${canEdit ? `<div class="menu-item" data-action="edit-headers">✎ Edit</div>` : ''}
                            ${reqHasEditedHeaders ? `<div class="menu-item" data-action="revert-headers">↩ Revert</div>` : ''}
                        </div>
                    </div>
                </div>
                <div class="content-block">
                    ${reqHasEditedHeaders || hasServerReqHeaders ? `
                    <div class="content-toolbar">
                        <div class="toolbar-left">
                            ${reqHasEditedHeaders ? `
                            <div class="body-tools-group">
                                <button class="body-tool body-content" data-action="set-header-content" data-content="original">Original</button>
                                <button class="body-tool body-content active" data-action="set-header-content" data-content="edited">Edited</button>
                            </div>` : `
                            <div class="body-tools-group">
                                <button class="body-tool body-content" data-action="set-header-content" data-content="original">Original</button>
                                <button class="body-tool body-content active" data-action="set-header-content" data-content="modified">Modified</button>
                            </div>`}
                        </div>
                        <div class="toolbar-right">
                            ${reqHasEditedHeaders ? '<span class="body-badge body-badge-edited">edited</span>' : ''}
                        </div>
                    </div>` : ''}
                    <div class="headers-container" data-target="request"
                         data-original-html="${escapeHtml(reqOriginalHtml)}"
                         data-edited-html="${escapeHtml(reqEditedHtml)}"
                         data-modified-html="${escapeHtml(reqModifiedHtml)}"
                         data-header-mode="${reqHasEditedHeaders ? 'edited' : (isModified && hasServerReqHeaders ? 'modified' : 'original')}">${reqHeadersHtml}</div>
                </div>
            </div>

            ${reqBodyHtml}
        </div>

        <div id="tab-response" class="tab-content" style="display:none">
            <div class="section-panel">
                <div class="section-header">
                    <span class="section-title">Response</span>
                </div>
                <div class="content-block">
                    <pre>Status: ${req.response ? req.response.status : (isDropped ? 'Dropped' : 'Pending')}</pre>
                </div>
            </div>

            ${isDropped ? `<div class="action-banner action-banner-drop">✕ Request was dropped — no response received</div>` : ''}

            <div class="section-panel">
                <div class="section-header">
                    <span class="section-title">Headers</span>
                    <div class="kebab" data-action="toggle-menu">
                        ⋮
                        <div class="kebab-menu">
                            <div class="menu-item" data-action="copy-headers" data-target="response">⧉ Copy</div>
                        </div>
                    </div>
                </div>
                <div class="content-block">
                    ${isMocked && Object.keys(serverRespHeaders).length > 0
                        ? (() => {
                            const serverRespRows = buildHeaderRows(serverRespHeaders);
                            const mockRespRows = req.response && req.response.headers
                                ? Object.entries(req.response.headers).map(([k,v]) => {
                                    const val = Array.isArray(v) ? v.join(', ') : v;
                                    return `<div class="header-row"><span class="header-key">${escapeHtml(k)}:</span><span class="header-value">${escapeHtml(val)}</span></div>`;
                                }).join('') || '<div style="color:#666">No headers</div>'
                                : '<div style="color:#666">No headers</div>';
                            return `<div class="content-toolbar">
                                <div class="toolbar-left">
                                    <div class="body-tools-group">
                                        <button class="body-tool body-content" data-action="set-header-content" data-target="response" data-content="original">Original</button>
                                        <button class="body-tool body-content active" data-action="set-header-content" data-target="response" data-content="mocked">Mocked</button>
                                    </div>
                                </div>
                                <div class="toolbar-right">
                                    <span class="body-badge body-badge-mocked">mocked</span>
                                </div>
                            </div>
                            <div class="headers-container" data-target="response" data-original-html="${escapeHtml(serverRespRows)}" data-mocked-html="${escapeHtml(mockRespRows)}" data-header-mode="mocked">${mockRespRows}</div>`;
                        })()
                        : `<div class="headers-container" data-target="response">${respHeaders}</div>`}
                </div>
            </div>

            ${respBodyHtml}
        </div>

        <div id="tab-origin" class="tab-content" style="display:none">
            <div class="section-panel">
                <div class="section-header">
                    <span class="section-title">Process</span>
                </div>
                <div class="content-block">
                    <div class="origin-info">
                        <div class="origin-row">
                            <span class="origin-label">Program:</span>
                            <span class="origin-value" id="originProgram">${escapeHtml(req.clientProcess || 'Unknown')}</span>
                        </div>
                        <div class="origin-row">
                            <span class="origin-label">PID:</span>
                            <span class="origin-value" id="originPID">${req.clientPid || 'N/A'}</span>
                        </div>
                        <div class="origin-row">
                            <span class="origin-label">Path:</span>
                            <span class="origin-value origin-path" id="originPath" title="${escapeHtml(req.clientPath || '')}">${escapeHtml(req.clientPath || 'N/A')}</span>
                        </div>
                        <div class="origin-row">
                            <span class="origin-label">Signed:</span>
                            <span class="origin-value" id="originSigned">
                                <span class="origin-status analyzing">Analyzing...</span>
                            </span>
                        </div>
                    </div>
                </div>
            </div>
        </div>

        ${replaysHtml}
    `;
    panel.dispatchEvent(new CustomEvent('detail-rendered'));
}

export function showTab(btn, tab) {
    document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
    document.querySelectorAll('.tab-content').forEach(c => c.style.display = 'none');
    btn.classList.add('active');
    document.getElementById('tab-' + tab).style.display = 'flex';

    if (tab === 'origin') {
        loadSignatureInfo();
    }
}

function loadSignatureInfo() {
    const pathEl = document.getElementById('originPath');
    if (!pathEl) return;
    const filePath = pathEl.getAttribute('title');
    if (!filePath) return;

    fetch(`/api/process/signature?path=${encodeURIComponent(filePath)}`)
        .then(r => r.json())
        .then(data => {
            const signedEl = document.getElementById('originSigned');
            if (!signedEl) return;

            if (data.status === 'analyzing') {
                signedEl.innerHTML = '<span class="origin-status analyzing">Analyzing...</span>';
            } else if (data.isSigned) {
                signedEl.innerHTML = `<span class="origin-status signed">✓ Signed by ${escapeHtml(data.signerName || 'Unknown')}</span>`;
            } else {
                signedEl.innerHTML = '<span class="origin-status unsigned">✗ Unsigned</span>';
            }
        })
        .catch(() => {
            const signedEl = document.getElementById('originSigned');
            if (signedEl) {
                signedEl.innerHTML = '<span class="origin-status unknown">Unable to verify</span>';
            }
        });
}

export function renderIgnoredList() {
    const list = document.getElementById('ignoredList');
    if (ignoredHosts.length === 0) {
        list.innerHTML = '<div style="padding:20px;color:#666;text-align:center">No ignored hosts</div>';
        return;
    }
    list.innerHTML = ignoredHosts.map(h => `
        <div class="ignored-item">
            <span class="host" title="${escapeHtml(h)}">${escapeHtml(h)}</span>
            <button class="remove-btn" data-action="unignore-item" data-host="${escapeHtml(h)}" title="Remove">&times;</button>
        </div>
    `).join('');
}

export function renderFocusedList() {
    const list = document.getElementById('focusedList');
    if (focusedHosts.length === 0) {
        list.innerHTML = '<div style="padding:20px;color:#666;text-align:center">No focused hosts</div>';
        return;
    }
    list.innerHTML = focusedHosts.map(h => `
        <div class="ignored-item">
            <span class="host" title="${escapeHtml(h)}">${escapeHtml(h)}</span>
            <button class="remove-btn" data-action="unfocus-item" data-host="${escapeHtml(h)}" title="Remove">&times;</button>
        </div>
    `).join('');
}

export function toggleIgnoredPanel() {
    document.getElementById('focusedPanel').classList.remove('open');
    document.getElementById('ignoredPanel').classList.toggle('open');
}

export function toggleFocusedPanel() {
    document.getElementById('ignoredPanel').classList.remove('open');
    document.getElementById('focusedPanel').classList.toggle('open');
}

export function renderRulesList() {
    const list = document.getElementById('rulesList');
    if (!list) return;
    if (rules.length === 0) {
        list.innerHTML = '<div style="padding:20px;color:#666;text-align:center">No rules defined</div>';
        return;
    }

    const actionIcons = {
        passthrough: '<span class="rule-action-icon rule-passthrough">→</span>',
        modify: '<span class="rule-action-icon rule-modify">✎</span>',
        mock: '<span class="rule-action-icon rule-mock">◉</span>',
        drop: '<span class="rule-action-icon rule-drop">✕</span>',
        response_mock: '<span class="rule-action-icon rule-response-mock">↻</span>',
    };

    list.innerHTML = rules.map(r => {
        const icon = actionIcons[r.action] || '';
        const actionLabel = r.action.replace('_', ' ');
        const enabledClass = r.enabled ? '' : ' rule-disabled';
        const matchParts = [];
        if (r.match.method) matchParts.push(r.match.method);
        if (r.match.host) matchParts.push(r.match.host);
        if (r.match.url_pattern) matchParts.push(r.match.url_pattern);
        const matchStr = matchParts.join(' ') || '*';

        let detail = '';
        if (r.action === 'mock') {
            detail = r.mock_response ? `Mock ${r.mock_response.status || 200}` : 'Mock';
        } else if (r.action === 'response_mock') {
            detail = r.mock_response ? `Mock ${r.mock_response.status || 200}` : 'Mock';
        } else if (r.action === 'drop') {
            detail = 'Block (timeout)';
        } else if (r.action === 'modify') {
            detail = 'Modify request';
        }

        return `<div class="rule-item${enabledClass}" data-rule-id="${r.id}">
            <div class="rule-item-main">
                ${icon}
                <span class="rule-match">${escapeHtml(matchStr)}</span>
                <span class="rule-detail">${escapeHtml(detail)}</span>
            </div>
            <div class="rule-item-actions">
                <button class="rule-toggle${r.enabled ? ' on' : ''}" data-action="toggle-rule" data-rule-id="${r.id}" title="${r.enabled ? 'Disable' : 'Enable'}">
                    <span class="rule-toggle-track"><span class="rule-toggle-thumb"></span></span>
                </button>
                <button class="rule-edit-btn" data-action="edit-rule" data-rule-id="${r.id}" title="Edit">${SVG_EDIT}</button>
                <button class="rule-delete-btn" data-action="delete-rule" data-rule-id="${r.id}" title="Delete">&times;</button>
            </div>
        </div>`;
    }).join('');
}

export function toggleRulesPanel() {
    document.getElementById('ignoredPanel').classList.remove('open');
    document.getElementById('focusedPanel').classList.remove('open');
    document.getElementById('rulesPanel').classList.toggle('open');
}

export function openRuleModal(rule) {
    const modal = document.getElementById('ruleModal');
    modal.dispatchEvent(new CustomEvent('modal-opening'));
    const title = document.getElementById('ruleModalTitle');
    modal.dataset.ruleId = rule?.id || '';
    title.textContent = rule?.id ? 'Edit Rule' : 'New Rule';
    document.getElementById('matchWarning').style.display = 'none';

    document.getElementById('ruleName').value = rule ? rule.name : '';
    document.getElementById('ruleHost').value = rule ? (rule.match.host || '') : '';
    document.getElementById('ruleUrl').value = rule ? (rule.match.url_pattern || '') : '';
    document.getElementById('ruleMethod').value = rule ? (rule.match.method || '') : '';

    const reqAction = (rule && rule.action === 'response_mock') ? 'passthrough' : (rule ? rule.action : 'passthrough');
    const reqRadio = document.querySelector(`input[name="ruleRequestAction"][value="${reqAction}"]`);
    reqRadio.checked = true;
    toggleRequestActionSections(reqAction);

    if (rule && rule.modified_request) {
        document.getElementById('modifyHost').value = rule.modified_request.host || '';
        document.getElementById('modifyUrl').value = rule.modified_request.url || '';
        renderInlineHeaders('modifyHeaders', rule.modified_request.headers || {});
    } else {
        document.getElementById('modifyHost').value = '';
        document.getElementById('modifyUrl').value = '';
        renderInlineHeaders('modifyHeaders', {});
    }

    if ((rule && rule.action === 'mock') || (rule && rule.action === 'response_mock')) {
        const mock = rule.mock_response || {};
        document.getElementById('mockRequestStatus').value = mock.status || 200;
        renderInlineHeaders('mockRequestHeaders', mock.headers || {});
    } else {
        document.getElementById('mockRequestStatus').value = 200;
        renderInlineHeaders('mockRequestHeaders', {});
    }

    const respAction = (rule && rule.action === 'response_mock') ? 'response_mock' : 'real';
    const respRadio = document.querySelector(`input[name="ruleResponseAction"][value="${respAction}"]`);
    respRadio.checked = true;
    toggleResponseActionSections(respAction);

    if (rule && rule.mock_response) {
        document.getElementById('mockResponseStatus').value = rule.mock_response.status || 200;
        renderInlineHeaders('mockResponseHeaders', rule.mock_response.headers || {});
    } else {
        document.getElementById('mockResponseStatus').value = 200;
        renderInlineHeaders('mockResponseHeaders', {});
    }

    modal.classList.add('open');

    const modifyBodyContainer = document.getElementById('modifyBodyEditor');
    if (modifyBodyContainer) modifyBodyContainer.dataset.initialBody = rule?.modified_request?.body || '';

    const mockReqBodyContainer = document.getElementById('mockRequestBodyEditor');
    if (mockReqBodyContainer) mockReqBodyContainer.dataset.initialBody = rule?.mock_response?.body || '';

    const mockRespBodyContainer = document.getElementById('mockResponseBodyEditor');
    if (mockRespBodyContainer) mockRespBodyContainer.dataset.initialBody = rule?.mock_response?.body || '';

    reqRadio.dispatchEvent(new Event('change'));
    respRadio.dispatchEvent(new Event('change'));
}

export function closeRuleModal() {
    document.getElementById('ruleModal').classList.remove('open');
}

function toggleRequestActionSections(action) {
    document.getElementById('modifyRequestSection').style.display = action === 'modify' ? '' : 'none';
    document.getElementById('mockRequestSection').style.display = action === 'mock' ? '' : 'none';
    document.getElementById('responseSection').style.display = (action === 'passthrough' || action === 'modify') ? '' : 'none';
}

function toggleResponseActionSections(action) {
    document.getElementById('mockResponseSection').style.display = action === 'response_mock' ? '' : 'none';
}

function renderInlineHeaders(containerId, headers) {
    const container = document.getElementById(containerId);
    if (!container) return;
    const entries = Object.entries(headers);
    if (entries.length === 0) {
        container.innerHTML = '<div class="inline-header-row"><input class="inline-header-key" placeholder="Key"><span class="header-colon">:</span><input class="inline-header-value" placeholder="Value"><button class="header-remove" title="Remove">&times;</button></div>';
        return;
    }
    const rows = [];
    for (const [k, v] of entries) {
        const vals = Array.isArray(v) ? v : [v];
        for (const val of vals) {
            rows.push(`<div class="inline-header-row"><input class="inline-header-key" value="${escapeHtml(k)}"><span class="header-colon">:</span><input class="inline-header-value" value="${escapeHtml(val)}"><button class="header-remove" title="Remove">&times;</button></div>`);
        }
    }
    container.innerHTML = rows.join('');
}

export function openRuleModalFromRequest(entry) {
    const urlPath = (entry.request.url || '').replace(/^https?:\/\/[^/]+/, '');
    const rule = {
        name: '',
        match: {
            method: entry.request.method || '',
            host: entry.request.host || '',
            url_pattern: urlPath,
        },
        action: 'mock',
        mock_response: {
            status: entry.response ? (entry.response.status || 200) : 200,
            headers: entry.response ? (entry.response.headers || {}) : {},
            body: entry.response ? (entry.response.body || '{}') : '{}',
        },
        modified_request: {
            host: entry.request.host || '',
            url: urlPath,
            headers: entry.request.headers || {},
            body: entry.request.body || '',
        },
    };
    openRuleModal(rule);
    document.querySelector('input[name="ruleRequestAction"][value="mock"]').checked = true;
    toggleRequestActionSections('mock');
    toggleResponseActionSections('real');
}
