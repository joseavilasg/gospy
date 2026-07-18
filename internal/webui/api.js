import { requests, setRequests, ignoredHosts, setIgnoredHosts, focusedHosts, setFocusedHosts, setFocusEnabled, lastTimestamp, setLastTimestamp } from './state.js';
import { renderList, renderIgnoredList, renderFocusedList, invalidateFilterCache } from './render.js';

export async function loadRequests() {
    try {
        const url = lastTimestamp ? `/api/requests?since=${encodeURIComponent(lastTimestamp)}` : '/api/requests';
        const resp = await fetch(url);
        const newItems = await resp.json();

        if (lastTimestamp && newItems.length > 0) {
            setRequests([...newItems, ...requests]);
        } else if (!lastTimestamp) {
            setRequests(newItems);
        }

        if (requests.length > 0) {
            setLastTimestamp(requests[0].timestamp);
        }

        invalidateFilterCache();
        renderList();
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
        document.getElementById('focusEnabled').checked = localStorage.getItem('gospy-focus-enabled') === 'true';
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
