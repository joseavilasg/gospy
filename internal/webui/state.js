let requestsMap = new Map();
export let requests = [];
export let selectedId = null;
export let filterText = '';
export let ignoredHosts = [];
export let focusedHosts = [];
export let focusEnabled = localStorage.getItem('gospy-focus-enabled') === 'true';
export let lastTimestamp = '';
export let processFilter = JSON.parse(localStorage.getItem('gospy-process-filter') || '[]');
export let signatureCache = {};

export function setRequests(val) {
    requestsMap.clear();
    for (const item of val) requestsMap.set(item.id, item);
    requests = val;
}
export function upsertRequests(newItems) {
    const existingIds = new Set(requestsMap.keys());
    for (const item of newItems) {
        if (requestsMap.has(item.id)) requestsMap.set(item.id, item);
    }
    const newOnly = newItems.filter(item => !existingIds.has(item.id));
    if (newOnly.length > 0) {
        requestsMap = new Map([...newOnly.map(i => [i.id, i]), ...requestsMap]);
    }
    requests = Array.from(requestsMap.values());
}
export function setSelectedId(val) { selectedId = val; }
export function setFilterText(val) { filterText = val; }
export function setIgnoredHosts(val) { ignoredHosts = val; }
export function setFocusedHosts(val) { focusedHosts = val; }
export function setFocusEnabled(val) {
    focusEnabled = val;
    localStorage.setItem('gospy-focus-enabled', val);
}
export function setLastTimestamp(val) { lastTimestamp = val; }
export function setProcessFilter(val) {
    processFilter = val;
    localStorage.setItem('gospy-process-filter', JSON.stringify(val));
}
export function setSignatureCache(val) { signatureCache = val; }

export let rules = [];
export function setRules(val) { rules = val; }
