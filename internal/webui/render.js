import { requests, selectedId, filterText, ignoredHosts, focusedHosts, focusEnabled, setSelectedId, rules, totalRequests, visibleCount, isReplayServed, isReplayComplete, getReplayMode } from './state.js';
import { isAnyFilterActive } from './filters.js';
import { detectBodyType, getKebabItems, renderContent, isEditable, getEntryData } from './body-types.js';

export const ITEM_HEIGHT = 35;
const BUFFER = 5;
let lastFiltered = [];
let lastRange = { start: -1, end: -1 };

export const SVG_EDIT = '<svg width="14" height="14" viewBox="0 0 16 16"><path d="M11.5 1.5l3 3L5 14H2v-3L11.5 1.5z" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/></svg>';
export const SVG_REVERT = '<svg width="14" height="14" viewBox="0 0 16 16"><path d="M3 7h7a3 3 0 010 6H8" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/><polyline points="6,4 3,7 6,10" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>';
export const SVG_MAXIMIZE = '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M8 3H5a2 2 0 0 0-2 2v3"/><path d="M21 8V5a2 2 0 0 0-2-2h-3"/><path d="M3 16v3a2 2 0 0 0 2 2h3"/><path d="M16 21h3a2 2 0 0 0 2-2v-3"/></svg>';
export const SVG_MINIMIZE = '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M8 3v3a2 2 0 0 1-2 2H3"/><path d="M21 8h-3a2 2 0 0 1-2-2V3"/><path d="M3 16h3a2 2 0 0 1 2 2v3"/><path d="M16 21v-3a2 2 0 0 1 2-2h3"/></svg>';
export const SVG_AGENT = '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 8V4H8"/><rect width="16" height="12" x="4" y="8" rx="2"/><path d="M2 14h2"/><path d="M20 14h2"/><path d="M15 13v2"/><path d="M9 13v2"/></svg>';
const SVG_RULE = '<svg width="16" height="16" viewBox="0 0 16 16" fill="none"><rect x="3" y="1" width="10" height="14" rx="1.5" stroke="currentColor" stroke-width="1.5"/><line x1="5.5" y1="5" x2="10.5" y2="5" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/><line x1="5.5" y1="8" x2="10.5" y2="8" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/><line x1="8" y1="10.5" x2="8" y2="13" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/><line x1="6.5" y1="11.75" x2="9.5" y2="11.75" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/></svg>';

export function escapeHtml(str) {
  if (!str) return '';
  return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

export function getFilteredRequests() {
  return requests;
}

export function invalidateFilterCache() { }

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

  const agentBadge = r.origin === 'agent'
    ? `<span class="agent-badge" title="Made by the agent">${SVG_AGENT}</span>`
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

  const streamBadge = r.stream
    ? '<span class="stream-badge" title="Streaming response — live">●</span>'
    : '';

  let serveBadge = '';
  if (isReplayServed(r.id)) {
    serveBadge = '<span class="serve-badge serve-badge-hit" title="Served in this replay run">✓</span>';
  } else if (isReplayComplete()) {
    serveBadge = '<span class="serve-badge serve-badge-miss" title="Never served in this replay run">✗</span>';
  }

  return `<div class="request-item${selected}" title="${escapeHtml(url)}" data-id="${r.id}"><span class="method method-${method}">${method}</span><span class="url">${escapeHtml(url)}</span>${status != null ? `<span class="status ${statusClass}">${status}</span>` : ''}${actionBadge}${replayBadge}${agentBadge}${streamBadge}${serveBadge}${processBadge}<span class="time">${time}</span></div>`;
}

export function renderList() {
  const list = document.getElementById('requestList');
  const filtered = getFilteredRequests();
  lastFiltered = filtered;
  const total = totalRequests;

  if (filterText || isAnyFilterActive() || (focusEnabled && focusedHosts.length > 0)) {
    document.getElementById('stats').textContent = visibleCount + ' / ' + total + ' requests';
  } else {
    document.getElementById('stats').textContent = total + ' requests';
  }

  if (visibleCount === 0) {
    list.innerHTML = total === 0
      ? '<div style="padding:20px;color:#666;text-align:center">Waiting for requests...</div>'
      : '<div style="padding:20px;color:#666;text-align:center">No matching requests</div>';
    lastRange = { start: -1, end: -1 };
    return;
  }

  if (filtered.length === 0) {
    list.innerHTML = '<div style="padding:20px;color:#666;text-align:center">Waiting for requests...</div>';
    lastRange = { start: -1, end: -1 };
    return;
  }

  lastRange = { start: -1, end: -1 };
  renderVisibleItems(list, filtered);
}

function renderVisibleItems(list, filtered) {
  if (!filtered) filtered = lastFiltered;
  if (!filtered || filtered.length === 0) return;

  const totalRows = visibleCount;
  const totalHeight = totalRows * ITEM_HEIGHT;
  const scrollTop = list.scrollTop;
  const viewportHeight = list.clientHeight || 600;
  const start = Math.max(0, Math.floor(scrollTop / ITEM_HEIGHT) - BUFFER);
  const end = Math.min(totalRows, Math.ceil((scrollTop + viewportHeight) / ITEM_HEIGHT) + BUFFER);

  if (start === lastRange.start && end === lastRange.end) return;
  lastRange = { start, end };

  const scrollTopSave = list.scrollTop;
  const visibleItems = filtered.slice(start, end);

  let html = `<div style="height:${totalHeight}px;position:relative">`;
  if (start > 0) html += `<div style="height:${start * ITEM_HEIGHT}px"></div>`;
  for (let i = 0; i < visibleItems.length; i++) {
    html += buildItemHtml(visibleItems[i]);
  }
  if (end < totalRows) html += `<div style="height:${(totalRows - end) * ITEM_HEIGHT}px"></div>`;
  html += '</div>';

  list.innerHTML = html;
  list.scrollTop = scrollTopSave;
}

export function onListScroll() {
  const list = document.getElementById('requestList');
  renderVisibleItems(list, lastFiltered);
}

export function clearListSelection() {
  const oldEl = document.querySelector('.request-item.selected');
  if (oldEl) oldEl.classList.remove('selected');
  setSelectedId(null);
}

export function selectRequest(id, activeTab = 'request') {
  clearReplayFeedSelection();
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
    .then(entry => renderDetail(entry, activeTab))
    .catch(e => console.error('Failed to load request detail:', e));
}

function buildHeaderRows(headers) {
  return Object.entries(headers).length > 0
    ? Object.entries(headers).map(([k, v]) => {
      const val = Array.isArray(v) ? v.join(', ') : v;
      const dataValues = Array.isArray(v) ? JSON.stringify(v) : JSON.stringify([v]);
      return `<div class="header-row" data-key="${escapeHtml(k)}" data-values='${escapeHtml(dataValues)}'><span class="header-key">${escapeHtml(k)}:</span><span class="header-value">${escapeHtml(val)}</span></div>`;
    }).join('')
    : '<div style="color:#666">No headers</div>';
}

function parseUrlParts(url) {
  let candidate = url;
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(url)) {
    candidate = url;
  } else if (url.startsWith('//')) {
    candidate = 'http:' + url;
  } else {
    candidate = 'http://' + url;
  }
  try {
    return new URL(candidate);
  } catch {
    try {
      return new URL(url, 'http://gospy.invalid');
    } catch {
      return null;
    }
  }
}

function shortUrl(url) {
  const u = parseUrlParts(url);
  return u ? u.pathname + u.search : url;
}

export function parseQueryString(url) {
  const u = parseUrlParts(url);
  if (!u) return [];
  const rows = [];
  u.searchParams.forEach((value, key) => rows.push({ key, value }));
  return rows;
}

function buildUrlBreakdown(method, url) {
  let protocol = '';
  let host = '';
  let path = '';
  const u = parseUrlParts(url);
  if (u) {
    protocol = u.protocol.slice(0, -1);
    host = u.host;
    path = u.pathname;
  } else {
    path = url.split('?')[0].split('#')[0];
  }
  return `<div class="url-breakdown">
      <div class="url-row"><span class="url-key">Method:</span><span class="url-value">${escapeHtml(method)}</span></div>
      <div class="url-row"><span class="url-key">Protocol:</span><span class="url-value">${escapeHtml(protocol)}</span></div>
      <div class="url-row"><span class="url-key">Host:</span><span class="url-value">${escapeHtml(host)}</span></div>
      <div class="url-row"><span class="url-key">Path:</span><span class="url-value">${escapeHtml(path)}</span></div>
    </div>`;
}

function buildQueryTable(url) {
  const rows = parseQueryString(url);
  const rowsHtml = rows.length > 0
    ? rows.map(r => `<div class="query-row"><span class="query-key">${escapeHtml(r.key)}:</span><span class="query-value">${escapeHtml(r.value)}</span></div>`).join('')
    : '<div class="query-empty">No query params</div>';
  return `<div class="query-table"><div class="query-title">Query params (${rows.length})</div>${rowsHtml}</div>`;
}

export function renderUrlViewInner(method, urlOriginal, urlModified, viewMode, contentMode) {
  const activeUrl = contentMode === 'modified' && urlModified ? urlModified : urlOriginal;
  const hasQuery = parseQueryString(activeUrl).length > 0;
  const toolbar = `
      <div class="content-toolbar">
        <div class="toolbar-left">
          <div class="body-tools-group">
            <button class="body-tool body-view${viewMode === 'pretty' ? ' active' : ''}" data-action="set-url-view" data-view="pretty">Pretty</button>
            <button class="body-tool body-view${viewMode === 'raw' ? ' active' : ''}" data-action="set-url-view" data-view="raw">Raw</button>
          </div>
          ${urlModified ? `<div class="divider-v"></div><div class="body-tools-group">
            <button class="body-tool body-content${contentMode === 'original' ? ' active' : ''}" data-action="set-url-content" data-content="original">Original</button>
            <button class="body-tool body-content${contentMode === 'modified' ? ' active' : ''}" data-action="set-url-content" data-content="modified">Modified</button>
          </div>` : ''}
        </div>
      </div>`;
  const viewHtml = viewMode === 'pretty'
    ? buildUrlBreakdown(method, activeUrl) + (hasQuery ? buildQueryTable(activeUrl) : '')
    : `<pre>${escapeHtml(method)} ${escapeHtml(activeUrl)}</pre>`;
  return toolbar + viewHtml;
}

export function buildRequestUrlBlock(method, urlOriginal, urlModified) {
  const contentMode = urlModified ? 'modified' : 'original';
  return `<div class="url-view" data-method="${escapeHtml(method)}" data-url-original="${escapeHtml(urlOriginal)}" data-url-modified="${escapeHtml(urlModified)}" data-view-mode="pretty" data-content-mode="${contentMode}">${renderUrlViewInner(method, urlOriginal, urlModified, 'pretty', contentMode)}</div>`;
}

function buildBodyViewer(target, entry, body, rawBody, compression, hasEdited, editedBody, contentType, isModified, modifiedBody, modifiedContentType, isMocked, mockedBody, mockedContentType, canEdit, bodyFile, bodySize, entryId, isBinaryBody, stream) {
  const badges = [];
  if (compression) badges.push(`<span class="body-badge body-badge-compression">${escapeHtml(compression)}</span>`);
  if (hasEdited) badges.push(`<span class="body-badge body-badge-edited">edited</span>`);
  if (isModified) badges.push(`<span class="body-badge body-badge-modified">modified</span>`);
  if (isMocked) badges.push(`<span class="body-badge body-badge-mocked">mocked</span>`);
  if (stream) badges.push(`<span class="body-badge body-badge-live">live</span>`);
  const badgesHtml = badges.join('');

  const bodyType = detectBodyType(contentType, entry, isBinaryBody);
  if (!isEditable(bodyType)) canEdit = false;

  const viewModeHtml = `<button class="body-tool body-view active" data-action="set-view" data-target="${target}" data-view="pretty">Pretty</button><button class="body-tool body-view" data-action="set-view" data-target="${target}" data-view="raw">Raw</button>`;

  const hasOtherContent = hasEdited || (isModified && modifiedBody) || (isMocked && mockedBody);
  const defaultContent = (isMocked && mockedBody) ? 'mocked' : 'original';
  const contentBtns = [];
  if (hasOtherContent) contentBtns.push(`<button class="body-tool body-content${defaultContent === 'original' ? ' active' : ''}" data-action="set-content" data-target="${target}" data-content="original">Original</button>`);
  if (hasEdited) contentBtns.push(`<button class="body-tool body-content" data-action="set-content" data-target="${target}" data-content="edited">Edited</button>`);
  if (isModified && modifiedBody) contentBtns.push(`<button class="body-tool body-content" data-action="set-content" data-target="${target}" data-content="modified">Modified</button>`);
  if (isMocked && mockedBody) contentBtns.push(`<button class="body-tool body-content${defaultContent === 'mocked' ? ' active' : ''}" data-action="set-content" data-target="${target}" data-content="mocked">Mocked</button>`);

  const menuItems = getKebabItems(bodyType, target, canEdit, hasEdited, entryId)
    .map(item => `<div class="menu-item" data-action="${item.action}" data-target="${item.target}"${item.entryId ? ` data-entry-id="${item.entryId}"` : ''}>${item.label}</div>`)
    .join('');

  const hasToolbar = badges.length > 0 || viewModeHtml.length > 0 || contentBtns.length > 0;
  const bodyHex = entry.bodyHex || '';

  const bodyContentHtml = renderContent(bodyType, target, {
    body, rawBody, compression, isModified, modifiedBody, isMocked, mockedBody,
    hasEdited, editedBody, defaultContent, bodyHex, bodySize, contentType,
    bodyTarget: target,
    ...getEntryData(entry, contentType, isBinaryBody),
  });

  return `<div class="section-panel" data-body-target="${target}" data-content-type="${escapeHtml(contentType)}">
        <div class="section-header">
            <span class="section-title">Body</span>
            ${menuItems.length > 0 ? `<div class="kebab" data-action="toggle-menu">
                ⋮
                <div class="kebab-menu">${menuItems}</div>
            </div>` : ''}
        </div>
        <div class="content-block">
            <div class="content-toolbar${hasToolbar ? '' : ' empty'}">
                <div class="toolbar-left">
                    ${viewModeHtml ? `<div class="body-tools-group">${viewModeHtml}</div>` : ''}
                    ${contentBtns.length > 0 ? `<div class="divider-v"></div><div class="body-tools-group">${contentBtns.join('')}</div>` : ''}
                </div>
                <div class="toolbar-right">${badgesHtml}<button class="fullscreen-btn" data-action="toggle-fullscreen-body" data-target="${target}" title="Full screen">${SVG_MAXIMIZE}</button></div>
            </div>
            <div class="body-scroll">${bodyContentHtml}</div>
        </div>
    </div>`;
}

export function buildResponseTab(req) {
  const isModified = req.appliedAction === 'modify';
  const isDropped = req.appliedAction === 'drop';
  const isMocked = req.appliedAction === 'mock' || req.appliedAction === 'response_mock';
  const canEdit = !isModified && !isMocked && !isDropped && !req?.response?.stream && !getReplayMode();

  const serverRespBody = req.serverResponse ? (req.serverResponse.body || '') : '';
  const serverRespHeaders = req.serverResponse ? (req.serverResponse.headers || {}) : {};
  const serverRespContentType = req.serverResponse?.headers?.['content-type']?.[0] || req.serverResponse?.headers?.['Content-Type']?.[0] || '';

  const respHeaders = req.response && req.response.headers ? Object.entries(req.response.headers).map(([k, v]) =>
    `<div class="header-row"><span class="header-key">${escapeHtml(k)}:</span><span class="header-value">${escapeHtml(Array.isArray(v) ? v.join(', ') : v)}</span></div>`
  ).join('') : '<div style="color:#666">No response yet</div>';

  const respBody = req.response ? (req.response.body || '') : '';
  const respRawBody = req.response ? (req.response.rawBody || '') : '';
  const respCompression = req.response ? (req.response.compression || '') : '';
  const respContentType = req.response?.headers?.['content-type']?.[0] || req.response?.headers?.['Content-Type']?.[0] || '';

  const respBodyFile = req.response?.bodyFile || '';
  const respBodySize = req.response?.bodySize || 0;
  const respIsBinary = req.response?.isBinaryBody || false;

  let respBodyHtml = '';
  if (respBody || respBodyFile) {
    const respHasEdited = req.response && req.response.editedBody && req.response.editedBody !== '';
    respBodyHtml = buildBodyViewer('response', req.response, respBody, respRawBody, respCompression, respHasEdited && canEdit, req.response.editedBody, respContentType, false, '', '', isMocked, serverRespBody, serverRespContentType, canEdit, respBodyFile, respBodySize, req.id, respIsBinary, !!req.response.stream);
  }

  return `
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
          ? Object.entries(req.response.headers).map(([k, v]) => {
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
    `;
}

let _replayEntryView = null;
export function setReplayEntryView(view) { _replayEntryView = view; }

function replayBreadcrumbHtml(view) {
  return `<div class="replay-breadcrumb" data-action="replay-back-event" title="Back to the replay event">
    <span class="replay-breadcrumb-back">← Back to replay (seq ${view.seq}, Match tab)</span>
    <span class="replay-breadcrumb-sep">·</span>
    <span class="replay-breadcrumb-muted">Viewing recorded entry · entry ${view.entry} · read-only</span>
  </div>`;
}

export function renderDetail(req, activeTab = 'request') {
  document.body.classList.remove('fullscreen-active');
  document.querySelectorAll('.section-panel.fullscreen-mode').forEach(p => p.classList.remove('fullscreen-mode'));
  const panel = document.getElementById('detailPanel');
  const replayEntryView = _replayEntryView;
  _replayEntryView = null;
  const host = req.request.host || '';
  const isIgnored = ignoredHosts.includes(host);
  const isFocused = focusedHosts.includes(host);

  const reqOriginalHeaders = req.request.headers || {};
  const reqEditedHeaders = req.request.editedHeaders;
  const reqHasEditedHeaders = reqEditedHeaders && Object.keys(reqEditedHeaders).length > 0;
  const isModified = req.appliedAction === 'modify';
  const serverReqHeaders = req.serverRequest ? (req.serverRequest.headers || {}) : {};
  const hasServerReqHeaders = isModified && Object.keys(serverReqHeaders).length > 0;

  const reqOriginalHtml = buildHeaderRows(reqOriginalHeaders);
  const reqEditedHtml = reqHasEditedHeaders ? buildHeaderRows(reqEditedHeaders) : '';
  const reqModifiedHtml = hasServerReqHeaders ? buildHeaderRows(serverReqHeaders) : '';
  const reqHeadersHtml = reqHasEditedHeaders ? reqEditedHtml : (isModified && hasServerReqHeaders ? reqModifiedHtml : reqOriginalHtml);

  const reqBody = req.request.body || '';
  const reqRawBody = req.request.rawBody || '';
  const reqCompression = req.request.compression || '';

  const reqContentType = req.request.headers?.['content-type']?.[0] || req.request.headers?.['Content-Type']?.[0] || '';

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

  const actionBanner = actionBannerHtml(req);

  const isMocked = req.appliedAction === 'mock' || req.appliedAction === 'response_mock';
  const isDropped = req.appliedAction === 'drop';

  const serverReqBody = req.serverRequest ? (req.serverRequest.body || '') : '';
  const serverReqContentType = req.serverRequest?.headers?.['content-type']?.[0] || req.serverRequest?.headers?.['Content-Type']?.[0] || '';

  const canEdit = !isModified && !isMocked && !isDropped && !getReplayMode();

  const reqBodyFile = req.request.bodyFile || '';
  const reqBodySize = req.request.bodySize || 0;
  const reqIsBinary = req.request.isBinaryBody || false;

  let reqBodyHtml = '';
  if (reqBody || reqBodyFile) {
    const reqHasEdited = req.request.editedBody && req.request.editedBody !== '';
    reqBodyHtml = buildBodyViewer('request', req.request, reqBody, reqRawBody, reqCompression, reqHasEdited, req.request.editedBody, reqContentType, isModified, serverReqBody, serverReqContentType, false, '', '', canEdit, reqBodyFile, reqBodySize, req.id, reqIsBinary);
  }

  let replayedFromHtml = '';
  if (req.replayedFrom) {
    const origEntry = requests.find(r => r.id === req.replayedFrom);
    if (origEntry) {
      replayedFromHtml = `<div class="replayed-from"><span class="replayed-from-icon">↻</span><span class="replayed-from-label">Replayed from:</span><a class="replayed-from-url" data-action="goto-replay" data-id="${req.replayedFrom}" title="${escapeHtml(origEntry.method)} ${escapeHtml(origEntry.url)}">${escapeHtml(origEntry.method)} ${escapeHtml(origEntry.url)}</a><span class="replayed-from-time">· ${new Date(origEntry.timestamp).toLocaleTimeString()}</span></div>`;
    } else {
      replayedFromHtml = `<div class="replayed-from"><span class="replayed-from-icon">↻</span><span class="replayed-from-label">Replayed from:</span><a class="replayed-from-url" data-action="goto-replay" data-id="${req.replayedFrom}">${req.replayedFrom.slice(0, 8)}</a></div>`;
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
        ${replayEntryView ? replayBreadcrumbHtml(replayEntryView) : ''}
        ${getReplayMode() ? '' : `
        <div class="detail-toolbar">
            ${ignoreBtn}
            ${focusBtn}
            <button class="btn-replay" data-action="send-replay"><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/></svg> Replay</button>
            <button class="btn-create-rule" data-action="create-rule-from-request">${SVG_RULE} Rule</button>
        </div>`}
        ${replayedFromHtml}
        <div class="tabs-row">
            <div class="tabs">
                <button class="tab ${activeTab === 'request' ? 'active' : ''}" data-action="tab" data-tab="request">Request</button>
                <button class="tab ${activeTab === 'response' ? 'active' : ''}" data-action="tab" data-tab="response">Response</button>
                <button class="tab ${activeTab === 'origin' ? 'active' : ''}" data-action="tab" data-tab="origin">Origin</button>
            </div>
            <div class="detail-id-group">
                <span class="detail-id">${escapeHtml(req.id)}</span>
                <button class="detail-id-copy" data-action="copy-id" title="Copy ID">${SVG_COPY_SMALL}</button>
            </div>
        </div>

        <div id="tab-request" class="tab-content" style="${activeTab !== 'request' ? 'display:none' : ''}">
            <div class="section-panel">
                <div class="section-header">
                    <span class="section-title">Request</span>
                    <div class="kebab" data-action="toggle-menu">
                        ⋮
                        <div class="kebab-menu">
                            <div class="menu-item" data-action="copy-curl">⎘ Copy as cURL</div>
                        </div>
                    </div>
                </div>
                <div class="content-block">
                  ${buildRequestUrlBlock(
    req.request.method,
    req.request.url || req.request.host,
    isModified && req.serverRequest ? (req.serverRequest.url || req.serverRequest.host) : ''
  )}
                </div>
            </div>

            <div class="section-panel">
                <div class="section-header">
                    <span class="section-title">Headers</span>
                    <div class="kebab" data-action="toggle-menu">
                        ⋮
                        <div class="kebab-menu">
                            <div class="menu-item" data-action="copy-headers" data-target="request">⧉ Copy</div>
                            ${canEdit ? `<div class="menu-item" data-action="edit-headers">✎ Edit</div>` : ''}
                            ${reqHasEditedHeaders && !getReplayMode() ? `<div class="menu-item" data-action="revert-headers">↩ Revert</div>` : ''}
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

        <div id="tab-response" class="tab-content" style="${activeTab === 'response' ? '' : 'display:none'}">
            ${buildResponseTab(req)}
        </div>

        <div id="tab-origin" class="tab-content" style="${activeTab === 'origin' ? '' : 'display:none'}">
            ${req.replayedFrom ? `
            <div class="section-panel">
                <div class="content-block" style="padding: var(--sp-10); text-align: center; color: var(--text-muted);">
                    Replayed by GoSpy - no client process
                </div>
            </div>
            ` : req.origin === 'agent' ? `
            <div class="section-panel">
                <div class="content-block" style="padding: var(--sp-10); text-align: center; color: var(--text-muted);">
                    Made by the agent - no client process
                </div>
            </div>
            ` : `
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
                                ${req.clientPath ? renderOriginStatus(req.clientSignature) : 'N/A'}
                            </span>
                        </div>
                    </div>
                </div>
            </div>
            `}
        </div>

        ${replaysHtml}
    `;
  panel.dispatchEvent(new CustomEvent('detail-rendered', { detail: { entry: req, activeTab } }));
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

export function renderOriginStatus(sig) {
  if (!sig || sig.status === 'analyzing') return '<span class="origin-status analyzing">Analyzing...</span>';
  if (sig.supported === false) return '<span class="origin-status unknown">N/A</span>';
  if (sig.isSigned) return `<span class="origin-status signed">✓ Signed by ${escapeHtml(sig.signerName || 'Unknown')}</span>`;
  return '<span class="origin-status unsigned">✗ Unsigned</span>';
}

export function loadSignatureInfo() {
  const pathEl = document.getElementById('originPath');
  if (!pathEl) return;
  const filePath = pathEl.getAttribute('title');
  if (!filePath) return;
  const signedEl = document.getElementById('originSigned');
  if (!signedEl) return;
  if (!signedEl.querySelector('.analyzing')) return;

  fetch(`/api/process/signature?path=${encodeURIComponent(filePath)}`)
    .then(r => r.json())
    .then(data => {
      const signedEl = document.getElementById('originSigned');
      if (!signedEl) return;
      signedEl.innerHTML = renderOriginStatus(data);
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

function closeAllPanels() {
  document.getElementById('ignoredPanel').classList.remove('open');
  document.getElementById('focusedPanel').classList.remove('open');
  document.getElementById('rulesPanel').classList.remove('open');
  document.getElementById('replayPanel').classList.remove('open');
}

export function toggleReplayPanel() {
  const panel = document.getElementById('replayPanel');
  const wasOpen = panel.classList.contains('open');
  closeAllPanels();
  if (!wasOpen) panel.classList.add('open');
}

export function toggleIgnoredPanel() {
  const panel = document.getElementById('ignoredPanel');
  const wasOpen = panel.classList.contains('open');
  closeAllPanels();
  if (!wasOpen) panel.classList.add('open');
}

export function toggleFocusedPanel() {
  const panel = document.getElementById('focusedPanel');
  const wasOpen = panel.classList.contains('open');
  closeAllPanels();
  if (!wasOpen) panel.classList.add('open');
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
  const panel = document.getElementById('rulesPanel');
  const wasOpen = panel.classList.contains('open');
  closeAllPanels();
  if (!wasOpen) panel.classList.add('open');
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

  const replayMode = getReplayMode();
  const reqAction = (() => {
    const raw = rule ? rule.action : 'passthrough';
    if (replayMode) return (raw === 'mock' || raw === 'drop') ? raw : 'mock';
    return raw === 'response_mock' ? 'passthrough' : raw;
  })();
  const reqRadio = document.querySelector(`input[name="ruleRequestAction"][value="${reqAction}"]`);
  reqRadio.checked = true;
  toggleRequestActionSections(reqAction);

  for (const v of ['passthrough', 'modify']) {
    const radio = document.querySelector(`input[name="ruleRequestAction"][value="${v}"]`);
    if (radio) radio.closest('.radio-label').style.display = replayMode ? 'none' : '';
  }

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
  for (const v of ['passthrough', 'modify']) {
    const radio = document.querySelector(`input[name="ruleRequestAction"][value="${v}"]`);
    if (radio) radio.closest('.radio-label').style.display = '';
  }
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

export function openRuleModalFromReplayEvent(ev) {
  const urlPath = (ev.url || '').replace(/^https?:\/\/[^/]+/, '');
  const rule = {
    name: '',
    match: {
      method: ev.method || '',
      host: (ev.request && ev.request.host) || '',
      url_pattern: urlPath,
    },
    action: 'mock',
    mock_response: {
      status: ev.status || 200,
      headers: {},
      body: '',
    },
    modified_request: {
      host: (ev.request && ev.request.host) || '',
      url: urlPath,
      headers: (ev.request && ev.request.headers) || {},
      body: (ev.request && ev.request.body) || '',
    },
  };
  openRuleModal(rule);
  document.querySelector('input[name="ruleRequestAction"][value="mock"]').checked = true;
  toggleRequestActionSections('mock');
  toggleResponseActionSections('real');
}

function actionLabel(action) {
  switch (action) {
    case 'mock': return '◉ Mocked';
    case 'response_mock': return '↻ Response Mocked';
    case 'drop': return '✕ Dropped';
    case 'modify': return '✎ Modified';
    default: return action;
  }
}

function actionBannerHtml(record) {
  if (!record.appliedAction || record.appliedAction === 'passthrough') return '';
  const ruleLabel = record.ruleName ? ` by "${escapeHtml(record.ruleName)}"` : '';
  const label = actionLabel(record.appliedAction);
  return `<div class="action-banner action-banner-${record.appliedAction}">${label}${ruleLabel}</div>`;
}

function replayEventRow(ev, selected) {
  const sel = selected ? ' selected' : '';
  const icon = ev.result === 'hit' ? '✓' : ev.result === 'miss' ? '✗' : '‼';
  const time = ev.ts ? `<span class="replay-event-time">${new Date(ev.ts).toLocaleTimeString()}</span>` : '';
  const ruleBadge = ev.appliedAction ? `<span class="replay-event-rule replay-event-rule-${escapeHtml(ev.appliedAction)}" title="${ev.ruleName ? `Rule: ${escapeHtml(ev.ruleName)}` : ''}">${actionLabel(ev.appliedAction)}</span>` : '';
  return `<div class="replay-event replay-event-${ev.result}${sel}" data-action="replay-event-detail" data-run="${escapeHtml(ev.runId)}" data-seq="${ev.seq}" title="${new Date(ev.ts).toLocaleString()}">
            <span class="replay-event-result">${icon}</span>
            <span class="replay-event-method">${escapeHtml(ev.method)}</span>
            <span class="replay-event-url">${escapeHtml(ev.url)}</span>
            ${ruleBadge}
            <span class="replay-event-seq">seq ${ev.seq}</span>
            ${time}
        </div>`;
}

const FEED_PAGE_BUFFER = 5;
let _feedEvents = [];
let _feedHasMore = false;
let _feedHeights = null;
let _feedOffsets = null;
let _feedLastRange = { start: -1, end: -1 };
let _feedSelectedRun = null;
let _feedSelectedSeq = null;
let _onFeedLoadOlder = () => { };

export function setOnReplayFeedLoadOlder(cb) { _onFeedLoadOlder = cb; }

function feedBlockHeight(ev, heights) {
  return heights.main;
}

function feedRowHeights() {
  if (_feedHeights) return _feedHeights;
  const feed = document.getElementById('replayFeed');
  let main = 31;
  if (feed) {
    const probe = document.createElement('div');
    probe.style.cssText = 'position:absolute;visibility:hidden;left:-9999px;top:0;pointer-events:none';
    feed.appendChild(probe);
    probe.innerHTML = replayEventRow({ result: 'hit', method: 'GET', url: 'x', ts: null, seq: 1 });
    if (probe.children[0]) main = probe.children[0].getBoundingClientRect().height || main;
    probe.remove();
  }
  _feedHeights = { main };
  return _feedHeights;
}

function feedDisplay() {
  return _feedEvents.slice().reverse();
}

function computeFeedOffsets(display, heights) {
  const offs = new Array(display.length + 1);
  let acc = 0;
  for (let i = 0; i < display.length; i++) {
    offs[i] = acc;
    acc += feedBlockHeight(display[i], heights);
  }
  offs[display.length] = acc;
  return offs;
}

// renderReplayFeed renders the virtual window of the feed into #replayFeed.
// opts.anchor carries {seq, vpOffset} captured before a live prepend so the
// row the user was reading stays in place while new events arrive above; when
// the user is at the top the feed stays pinned to 0 (the newest event).
export function renderReplayFeed(opts) {
  const feed = document.getElementById('replayFeed');
  if (!feed) return;
  if (_feedEvents.length === 0) {
    feed.innerHTML = '<div class="replay-event-empty">No replay activity yet — requests will appear here as the replay server serves them.</div>';
    _feedOffsets = null;
    _feedLastRange = { start: -1, end: -1 };
    return;
  }
  const heights = feedRowHeights();
  const display = feedDisplay();
  const offsets = computeFeedOffsets(display, heights);
  _feedOffsets = offsets;
  const totalHeight = offsets[offsets.length - 1];
  const viewportHeight = feed.clientHeight || 300;

  const scrollTop = feed.scrollTop;
  const top = Math.max(0, scrollTop - FEED_PAGE_BUFFER * heights.main);
  const bottom = scrollTop + viewportHeight + FEED_PAGE_BUFFER * heights.main;
  let start = 0;
  while (start < display.length && offsets[start + 1] <= top) start++;
  let end = display.length - 1;
  while (end >= 0 && offsets[end] >= bottom) end--;
  end = Math.max(start, end);

  const anchor = (opts && opts.anchor) || null;
  if (!opts && start === _feedLastRange.start && end === _feedLastRange.end) return;
  _feedLastRange = { start, end };

  const scrollTopSave = feed.scrollTop;
  let html = `<div style="height:${totalHeight}px;position:relative">`;
  if (start > 0) html += `<div style="height:${offsets[start]}px"></div>`;
  for (let i = start; i <= end; i++) {
    const isSel = display[i].seq === _feedSelectedSeq && display[i].runId === _feedSelectedRun;
    html += replayEventRow(display[i], isSel);
  }
  if (end < display.length - 1) html += `<div style="height:${totalHeight - offsets[end + 1]}px"></div>`;
  html += '</div>';
  feed.innerHTML = html;

  if (anchor) {
    const idx = display.findIndex(e => e.seq === anchor.seq);
    feed.scrollTop = idx >= 0 ? Math.max(0, offsets[idx] - anchor.vpOffset) : scrollTopSave;
  } else if (scrollTopSave <= 0) {
    feed.scrollTop = 0;
  } else {
    feed.scrollTop = scrollTopSave;
  }
}

export function setReplayFeed(events, hasMore) {
  const feed = document.getElementById('replayFeed');
  if (feed) feed.scrollTop = 0;
  _feedEvents = events;
  _feedHasMore = hasMore;
  _feedOffsets = null;
  _feedLastRange = { start: -1, end: -1 };
  _feedSelectedRun = null;
  _feedSelectedSeq = null;
  renderReplayFeed();
}

export function prependReplayFeed(older, hasMore) {
  _feedEvents = older.concat(_feedEvents);
  _feedHasMore = hasMore;
  _feedOffsets = null;
  _feedLastRange = { start: -1, end: -1 };
  renderReplayFeed();
}

export function appendReplayFeedEvent(ev) {
  const feed = document.getElementById('replayFeed');
  let anchor = null;
  if (feed && feed.scrollTop > 0 && _feedOffsets && _feedEvents.length > 0) {
    const row = feed.querySelector('.replay-event');
    if (row) {
      const seq = parseInt(row.dataset.seq, 10);
      if (Number.isFinite(seq)) {
        const oldDisplay = _feedEvents.slice().reverse();
        const idx = oldDisplay.findIndex(e => e.seq === seq);
        if (idx >= 0) anchor = { seq, vpOffset: _feedOffsets[idx] - feed.scrollTop };
      }
    }
  }
  _feedEvents.push(ev);
  _feedOffsets = null;
  _feedLastRange = { start: -1, end: -1 };
  renderReplayFeed({ anchor });
}

export function clearReplayFeed() {
  _feedEvents = [];
  _feedHasMore = false;
  _feedOffsets = null;
  _feedLastRange = { start: -1, end: -1 };
  _feedSelectedRun = null;
  _feedSelectedSeq = null;
  renderReplayFeed();
}

export function selectReplayFeedEvent(run, seq) {
  _feedSelectedRun = run;
  _feedSelectedSeq = seq;
  renderReplayFeed({});
}

export function clearReplayFeedSelection() {
  const feed = document.getElementById('replayFeed');
  if (feed) {
    const el = feed.querySelector('.replay-event.selected');
    if (el) el.classList.remove('selected');
  }
  _feedSelectedRun = null;
  _feedSelectedSeq = null;
}

export function onReplayFeedScroll() {
  const feed = document.getElementById('replayFeed');
  if (!feed || !_feedOffsets) return;
  renderReplayFeed();
  if (!_feedHasMore || _feedEvents.length === 0) return;
  const totalHeight = _feedOffsets[_feedOffsets.length - 1];
  const viewportHeight = feed.clientHeight || 300;
  if (feed.scrollTop + viewportHeight >= totalHeight - viewportHeight) {
    _onFeedLoadOlder(_feedEvents[0].seq);
  }
}

export function renderReplayEventDetail(detail, activeTab = 'match') {
  clearListSelection();
  _replayEntryView = null;
  const panel = document.getElementById('detailPanel');
  if (!panel || !detail?.event) return;
  const ev = detail.event;
  const req = ev.request || {};
  const headers = req.headers || {};

  const icon = ev.result === 'hit' ? '✓' : ev.result === 'miss' ? '✗' : '‼';

  const requestBodyHtml = (() => {
    if (req.body) return `<pre>${escapeHtml(req.body)}</pre>`;
    if (req.bodyFile) {
      if (ev.entryId) {
        return `<a class="replay-entry-link" data-action="replay-entry-body" data-run="${escapeHtml(ev.runId)}" data-id="${escapeHtml(ev.entryId)}" data-target="request" href="#">Load body (${req.bodySize || ''} bytes)</a>`;
      }
      return `<a class="replay-entry-link" data-action="replay-body" data-run="${escapeHtml(ev.runId)}" data-seq="${ev.seq}" href="#">Load body (${req.bodySize || ''} bytes)</a>`;
    }
    if (req.isBinaryBody) return `<pre>Binary body — ${req.bodySize || ''} bytes</pre>`;
    return '<div class="replay-event-empty">No body</div>';
  })();

  panel.innerHTML = `
        ${actionBannerHtml(ev)}
        <div class="detail-toolbar">
            <button class="btn-create-rule" data-action="create-rule-from-replay-event" title="Create rule from this event">${SVG_RULE} Rule</button>
        </div>
        <div class="tabs-row">
            <div class="tabs">
                <button class="tab${activeTab === 'request' ? ' active' : ''}" data-action="tab" data-tab="request">Request</button>
                <button class="tab${activeTab === 'response' ? ' active' : ''}" data-action="tab" data-tab="response">Response</button>
                <button class="tab${activeTab === 'origin' ? ' active' : ''}" data-action="tab" data-tab="origin">Origin</button>
                <button class="tab${activeTab === 'match' ? ' active' : ''}" data-action="tab" data-tab="match">Match</button>
            </div>
            <div class="detail-id-group"><span class="replay-badge replay-badge-${ev.result || 'exhausted'}">${icon} ${escapeHtml((ev.result || '').toUpperCase())}</span></div>
        </div>

        <div id="tab-request" class="tab-content" style="${activeTab === 'request' ? '' : 'display:none'}">
            <div class="section-panel">
                <div class="section-header"><span class="section-title">Request</span></div>
                <div class="content-block">
                    <div class="url-view" data-method="${escapeHtml(ev.method)}" data-url-original="${escapeHtml(ev.url)}" data-url-modified="" data-view-mode="pretty" data-content-mode="original">${renderUrlViewInner(ev.method, ev.url, '', 'pretty', 'original')}</div>
                </div>
            </div>
            <div class="section-panel">
                <div class="section-header"><span class="section-title">Headers</span></div>
                <div class="content-block"><div class="headers-container">${buildHeaderRows(headers)}</div></div>
            </div>
            ${req.body || req.bodyFile || req.isBinaryBody ? `<div class="section-panel">
                <div class="section-header"><span class="section-title">Body</span></div>
                <div class="content-block">${requestBodyHtml}</div>
            </div>` : ''}
        </div>

        <div id="tab-response" class="tab-content" style="${activeTab === 'response' ? '' : 'display:none'}">
            ${buildReplayResponseTab(detail)}
        </div>

        <div id="tab-origin" class="tab-content" style="${activeTab === 'origin' ? '' : 'display:none'}">
            <div class="section-panel">
                <div class="section-header"><span class="section-title">Origin</span></div>
                <div class="content-block">
                    <div class="origin-info">
                        <div class="origin-row">
                            <span class="origin-label">Program:</span>
                            <span class="origin-value" id="originProgram">${escapeHtml(ev.clientProcess || 'Unknown')}</span>
                        </div>
                        <div class="origin-row">
                            <span class="origin-label">PID:</span>
                            <span class="origin-value" id="originPID">${ev.clientPid || 'N/A'}</span>
                        </div>
                        <div class="origin-row">
                            <span class="origin-label">Path:</span>
                            <span class="origin-value origin-path" id="originPath" title="${escapeHtml(ev.clientPath || '')}">${escapeHtml(ev.clientPath || 'N/A')}</span>
                        </div>
                        <div class="origin-row">
                            <span class="origin-label">Signed:</span>
                            <span class="origin-value" id="originSigned">
                                ${ev.clientPath ? renderOriginStatus(detail.clientSignature) : 'N/A'}
                            </span>
                        </div>
                    </div>
                </div>
            </div>
        </div>

        <div id="tab-match" class="tab-content" style="${activeTab === 'match' ? '' : 'display:none'}">
            <div id="replayMatchContainer"><div class="replay-event-empty">Loading candidates…</div></div>
        </div>`;
}

function buildReplayResponseTab(detail) {
  const ev = detail.event;
  const status = ev.status != null ? ev.status : '—';
  const res = detail.matchedEntry?.response;
  const srv = ev.servedResponse;

  let statusHtml = `<pre>${status}</pre>`;
  if (ev.result === 'miss') statusHtml = `<pre>${status} — replay miss</pre>`;
  if (ev.result === 'exhausted') statusHtml = `<pre>${status} — replay exhausted</pre>`;

  let headersHtml;
  let bodyHtml = '';
  if (srv) {
    headersHtml = buildHeaderRows(srv.headers || {});
    if (srv.body) bodyHtml = `<pre>${escapeHtml(srv.body)}</pre>`;
    else if (srv.bodyFile) bodyHtml = `<a class="replay-entry-link" data-action="replay-body" data-run="${escapeHtml(ev.runId)}" data-seq="${ev.seq}" data-target="served" href="#">Download body (${srv.bodySize || ''} bytes)</a>`;
  } else if (ev.result === 'hit' && res) {
    const resHeaders = res.headers || {};
    headersHtml = buildHeaderRows(resHeaders) +
      `<div class="header-row"><span class="header-key">X-Gospy-Replay:</span><span class="header-value">hit</span></div>`;
    if (res.body) bodyHtml = `<pre>${escapeHtml(res.body)}</pre>`;
    else if (res.bodyFile && ev.entryId) bodyHtml = `<a class="replay-entry-link" data-action="replay-entry-body" data-run="${escapeHtml(ev.runId)}" data-id="${escapeHtml(ev.entryId)}" data-target="response" href="#">Download body (${res.bodySize || ''} bytes)</a>`;
  } else {
    headersHtml = `<div class="header-row"><span class="header-key">X-Gospy-Replay:</span><span class="header-value">${escapeHtml(ev.result)}</span></div>`;
    if (detail.syntheticBody) bodyHtml = `<pre>${escapeHtml(detail.syntheticBody)}</pre>`;
  }

  return `
        <div class="section-panel">
            <div class="section-header"><span class="section-title">Status</span></div>
            <div class="content-block">${statusHtml}</div>
        </div>
        <div class="section-panel">
            <div class="section-header"><span class="section-title">Headers</span></div>
            <div class="content-block"><div class="headers-container">${headersHtml}</div></div>
        </div>
        ${bodyHtml ? `<div class="section-panel">
            <div class="section-header"><span class="section-title">Body</span></div>
            <div class="content-block">${bodyHtml}</div>
        </div>` : ''}
    `;
}

export function renderReplayMatch(resp, ctx, keepScroll) {
  const container = document.getElementById('replayMatchContainer');
  if (!container) return;
  if (!resp) {
    container.innerHTML = '<div class="replay-event-empty">No candidates available for this event.</div>';
    return;
  }

  const entries = resp.entries || [];
  const listEl = container.querySelector('.match-candidate-list');
  const scrollPos = keepScroll && listEl ? listEl.scrollTop : 0;

  const total = resp.total || {};
  const result = (ctx && ctx.result) || '';
  const seq = (ctx && ctx.seq) || 0;
  const selected = entries.find(c => c.entryId === resp.selectedEntryId);
  const ignored = (resp.matchConfig && resp.matchConfig.ignore_query_params) || [];

  let title = 'Select a candidate to compare';
  if (result === 'hit') title = `Matched · seq ${seq} · recorded`;
  else if (total.matching === 0 && resp.scope === 'all') title = 'No candidate shares this host+path — showing all pending';

  const ignoredNote = ignored.length > 0
    ? `<span class="replay-config-note" title="ignore_query_params used for this run">ignoring: ${escapeHtml(ignored.join(', '))}</span>`
    : '';

  const segHtml = `
    <div class="match-scope-head">
        <div class="match-scope-seg">
            <span class="match-scope-btn${resp.scope !== 'all' ? ' active' : ''}" data-action="replay-scope" data-scope="matching">Matching (${total.matching ?? 0})</span>
            <span class="match-scope-btn${resp.scope === 'all' ? ' active' : ''}" data-action="replay-scope" data-scope="all">All pending (${total.pending ?? 0})</span>
        </div>
    </div>`;

  const rowsHtml = buildCandidateRows(entries, resp.scope, resp.selectedEntryId);

  const listHtml = `
    <div class="match-list-col">
        ${segHtml}
        <div class="match-search-wrap">
            <input class="match-search" type="search" data-action="replay-search" placeholder="Search by entry, url..." value="${escapeHtml(resp.q || '')}">
            <button class="match-search-clear${resp.q ? '' : ' hidden'}" data-action="replay-search-clear" title="Clear search"><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg></button>
        </div>
        <div class="match-candidate-list">${rowsHtml}</div>
    </div>`;

  const consumedHtml = resp.consumed
    ? `<div class="replay-warn-box">
        <span class="replay-warn-icon">⚠</span>
        <p class="replay-warn-text">The entry ${resp.consumed.entry} was already consumed by <b>seq ${resp.consumed.consumedBySeq}</b>. Likely a duplicate or out-of-order request — not an ignore_query_params issue.
        <br><span class="replay-warn-link" data-action="replay-warn-entry">View that entry (seq ${resp.consumed.consumedBySeq}) →</span></p>
    </div>`
    : '';

  const compareLine = selected
    ? `<div class="match-compare-line">
        <span class="match-compare-label">Comparing against entry ${selected.entry} · ${escapeHtml(replayCandidateTagText(selected))}</span>
        <span class="nav-link" data-action="replay-full-entry">View full entry →</span>
    </div>`
    : '';

  const diffHtml = resp.diff
    ? renderReplayDiff(resp.diff, selected)
    : '<div class="replay-event-empty">Select a candidate to see its diff.</div>';

  container.innerHTML = `
        <div class="replay-section-title">${escapeHtml(title)}${ignoredNote}</div>
        ${consumedHtml}
        <div class="match-layout">
            ${listHtml}
            <div class="match-diff-col">
                ${compareLine}
                ${diffHtml}
            </div>
        </div>`;

  if (scrollPos > 0) {
    const newList = container.querySelector('.match-candidate-list');
    if (newList) newList.scrollTop = scrollPos;
  }
}

function buildCandidateRows(entries, scope, selectedId) {
  if (!entries || entries.length === 0) return '<div class="match-empty">No candidates.</div>';
  return entries.map(c => {
    const rowClass = c.entryId === selectedId ? ' selected' : '';
    const stateClass = scope !== 'all' && c.tag ? ` ${c.tag}` : '';
    const tagHtml = scope !== 'all' && c.tag ? replayCandidateTag(c) : '';
    return `<div class="match-candidate-row${rowClass}${stateClass}" data-action="replay-candidate" data-entry="${escapeHtml(c.entryId)}" title="${escapeHtml(c.url)}">
        <div class="match-candidate-top">
            <span class="match-candidate-name">entry ${c.entry}</span>
            ${tagHtml}
        </div>
        <span class="match-candidate-url" title="${escapeHtml(c.url)}">${escapeHtml(shortUrl(c.url))}</span>
    </div>`;
  }).join('');
}

// renderMatchCandidates re-renders only the candidate rows, leaving the search
// input and scope control untouched - the search path must not rebuild static
// chrome or the input would lose its text and focus. Falls back to the full
// render when the list is not mounted yet.
export function renderMatchCandidates(resp, ctx) {
  const list = document.querySelector('.match-candidate-list');
  if (!list) {
    renderReplayMatch(resp, ctx);
    return;
  }
  list.innerHTML = buildCandidateRows(resp.entries, resp.scope, resp.selectedEntryId);
}

function replayCandidateTagText(c) {
  if (c.tag === 'served') return '✓ matched';
  if (c.tag === 'consumed') return `consumed by seq ${c.consumedBySeq}`;
  if (c.diffCount != null && c.diffCount > 0) return `${c.diffCount} diff${c.diffCount === 1 ? '' : 's'}`;
  if (c.diffCount != null) return 'exact match';
  return 'pending';
}

function replayCandidateTag(c) {
  let cls = 'replay-tag-pending';
  if (c.tag === 'served') cls = 'replay-tag-served';
  else if (c.tag === 'consumed') cls = 'replay-tag-consumed';
  return `<span class="replay-tag ${cls}">${escapeHtml(replayCandidateTagText(c))}</span>`;
}

export function renderReplayDiff(diff, selected) {
  if (!diff) return '<div class="replay-event-empty">No diff available.</div>';
  const headerCol = selected ? `entry ${selected.entry}` : 'Recorded';
  const headerRow = `<div class="diff-header-row"><span>Param</span><span>Incoming</span><span>${escapeHtml(headerCol)}</span><span>Status</span></div>`;

  const hp = diff.hostPath || {};
  const hpClass = hp.match ? 'match' : 'mismatch';
  const hostPathRow = `<div class="diff-row diff-row-${hpClass}">
      <span class="diff-side">host+path</span>
      <span class="diff-incoming" title="${escapeHtml(hp.incoming || '')}">${escapeHtml(hp.incoming || '—')}</span>
      <span class="diff-recorded" title="${escapeHtml(hp.recorded || '')}">${escapeHtml(hp.recorded || '—')}</span>
      <span class="diff-status diff-status-${hpClass}">${hp.match ? '✓ match' : '✗ mismatch'}</span>
  </div>`;

  const paramsHtml = (diff.params || []).map(p => {
    const cls = p.status === 'match' ? 'match' : p.status === 'ignored' ? 'ignored' : 'mismatch';
    const incoming = p.incoming != null ? escapeHtml(p.incoming) : null;
    const recorded = p.recorded != null ? escapeHtml(p.recorded) : null;
    const cell = (v, c) => v != null
      ? `<span class="${c}" title="${v}">${v}</span>`
      : `<span class="${c}"><span class="diff-empty">—</span></span>`;
    let statusText = '✓ match';
    if (p.status === 'mismatch') statusText = '✗ mismatch';
    else if (p.status === 'missing_in_incoming') statusText = 'missing in incoming';
    else if (p.status === 'missing_in_recorded') statusText = 'missing in recorded';
    else if (p.status === 'ignored') statusText = 'ignored by config';
    return `<div class="diff-row diff-row-${cls}">
        <span class="diff-side">${escapeHtml(p.key)}</span>
        ${cell(incoming, 'diff-incoming')}
        ${cell(recorded, 'diff-recorded')}
        <span class="diff-status diff-status-${cls}">${statusText}</span>
    </div>`;
  }).join('');

  return `<div class="diff-table">${headerRow}${hostPathRow}${paramsHtml}</div>`;
}
