import { requests, selectedId, filterText, ignoredHosts, focusedHosts, focusEnabled, setSelectedId } from './state.js';

export function escapeHtml(str) {
    if (!str) return '';
    return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function hostMatchesFocus(host) {
    if (!focusEnabled || focusedHosts.length === 0) return true;
    for (const pattern of focusedHosts) {
        if (pattern === host) return true;
        if (pattern.includes('*')) {
            const regex = '^' + pattern.replace(/[.+?^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '.*') + '$';
            if (new RegExp(regex).test(host)) return true;
        }
    }
    return false;
}

export function getFilteredRequests() {
    let result = requests.filter(r => hostMatchesFocus(r.request.host));

    if (!filterText) return result;
    const q = filterText.toLowerCase();
    return result.filter(r => {
        const method = (r.request.method || '').toLowerCase();
        const url = (r.request.url || r.request.host || '').toLowerCase();
        const status = r.response ? String(r.response.status) : '';
        return method.includes(q) || url.includes(q) || status.includes(q);
    });
}

export function renderList() {
    const list = document.getElementById('requestList');
    const filtered = getFilteredRequests();
    const total = requests.length;

    if (filterText || (focusEnabled && focusedHosts.length > 0)) {
        document.getElementById('stats').textContent = filtered.length + ' / ' + total + ' requests';
    } else {
        document.getElementById('stats').textContent = total + ' requests';
    }

    if (requests.length === 0) {
        list.innerHTML = '<div style="padding:20px;color:#666;text-align:center">Waiting for requests...</div>';
        return;
    }

    const scrollTop = list.scrollTop;

    list.innerHTML = filtered.map(r => {
        const method = r.request.method;
        const url = r.request.url || r.request.host;
        const status = r.response ? r.response.status : null;
        const time = new Date(r.timestamp).toLocaleTimeString();
        const selected = r.id === selectedId ? ' selected' : '';
        const statusClass = status ? (status < 300 ? 'status-2xx' : status < 400 ? 'status-3xx' : status < 500 ? 'status-4xx' : 'status-5xx') : '';

        return `
            <div class="request-item${selected}" title="${escapeHtml(url)}" data-id="${r.id}">
                <span class="method method-${method}">${method}</span>
                <span class="url">${escapeHtml(url)}</span>
                ${status ? `<span class="status ${statusClass}">${status}</span>` : ''}
                <span class="time">${time}</span>
            </div>
        `;
    }).join('');

    if (selectedId) {
        list.scrollTop = scrollTop;
    } else {
        list.scrollTop = 0;
    }
}

export function selectRequest(id) {
    setSelectedId(id);
    renderList();
    const req = requests.find(r => r.id === id);
    if (req) renderDetail(req);
}

export function renderDetail(req) {
    const panel = document.getElementById('detailPanel');
    const host = req.request.host || '';
    const isIgnored = ignoredHosts.includes(host);
    const isFocused = focusedHosts.includes(host);

    const reqHeaders = req.request.headers ? Object.entries(req.request.headers).map(([k,v]) =>
        `<div class="header-row"><span class="header-key">${escapeHtml(k)}:</span><span class="header-value">${escapeHtml(Array.isArray(v) ? v.join(', ') : v)}</span></div>`
    ).join('') : '<div style="color:#666">No headers</div>';

    const respHeaders = req.response && req.response.headers ? Object.entries(req.response.headers).map(([k,v]) =>
        `<div class="header-row"><span class="header-key">${escapeHtml(k)}:</span><span class="header-value">${escapeHtml(Array.isArray(v) ? v.join(', ') : v)}</span></div>`
    ).join('') : '<div style="color:#666">No response yet</div>';

    const reqBody = req.request.body || '';
    const respBody = req.response ? (req.response.body || '') : '';

    const ignoreBtn = isIgnored
        ? `<button class="btn-active" data-action="unignore" data-host="${escapeHtml(host)}"><svg width="12" height="12" viewBox="0 0 16 16"><polyline points="3,8 7,12 13,4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg> Remove ignore</button>`
        : `<button class="btn-ignore-detail" data-action="ignore" data-host="${escapeHtml(host)}"><svg width="12" height="12" viewBox="0 0 16 16"><circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" stroke-width="2"/><line x1="5" y1="5" x2="11" y2="11" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg> Ignore host</button>`;

    let focusBtn;
    if (isFocused) {
        focusBtn = `<button class="btn-active btn-focus-active" data-action="unfocus" data-host="${escapeHtml(host)}"><svg width="12" height="12" viewBox="0 0 16 16"><polyline points="3,8 7,12 13,4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg> Focused</button>`;
    } else {
        focusBtn = `<button class="btn-focus-detail" data-action="focus" data-host="${escapeHtml(host)}"><svg width="12" height="12" viewBox="0 0 16 16"><circle cx="8" cy="8" r="7" fill="none" stroke="currentColor" stroke-width="2"/><circle cx="8" cy="8" r="3" fill="currentColor"/></svg> Add to focus</button>`;
    }

    panel.innerHTML = `
        <div class="detail-toolbar">
            ${ignoreBtn}
            ${focusBtn}
        </div>
        <div class="tabs">
            <button class="tab active" data-action="tab" data-tab="request">Request</button>
            <button class="tab" data-action="tab" data-tab="response">Response</button>
        </div>

        <div id="tab-request" class="tab-content">
            <div class="section-title">Request</div>
            <pre>${escapeHtml(req.request.method)} ${escapeHtml(req.request.url || req.request.host)}</pre>

            <div class="section-title" style="margin-top:12px">Headers</div>
            <pre>${reqHeaders}</pre>

            ${reqBody ? `<div class="section-title" style="margin-top:12px">Body</div><pre>${escapeHtml(reqBody)}</pre>` : ''}
        </div>

        <div id="tab-response" class="tab-content" style="display:none">
            <div class="section-title">Response</div>
            <pre>Status: ${req.response ? req.response.status : 'Pending'}</pre>

            <div class="section-title" style="margin-top:12px">Headers</div>
            <pre>${respHeaders}</pre>

            ${respBody ? `<div class="section-title" style="margin-top:12px">Body</div><pre>${escapeHtml(respBody)}</pre>` : ''}
        </div>
    `;
}

export function showTab(btn, tab) {
    document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
    document.querySelectorAll('.tab-content').forEach(c => c.style.display = 'none');
    btn.classList.add('active');
    document.getElementById('tab-' + tab).style.display = 'block';
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
    document.getElementById('ignoredPanel').classList.toggle('open');
}

export function toggleFocusedPanel() {
    document.getElementById('focusedPanel').classList.toggle('open');
}
