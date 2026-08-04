let requestsMap = new Map();
export let requests = [];
export let selectedId = null;
export let filterText = '';
export let ignoredHosts = [];
export let focusedHosts = [];
export let focusEnabled = false;
export let agentPreview = false;
export let agentEnabled = false;
export let agentExposed = false;
export let lastTimestamp = '';
export let signatureCache = {};
export let criteriaVersion = null;
export let totalRequests = 0;
export let visibleCount = 0;

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
export function removeRequests(ids) {
    for (const id of ids) requestsMap.delete(id);
    requests = Array.from(requestsMap.values());
}
export function setSelectedId(val) { selectedId = val; }
export function setFilterText(val) { filterText = val; }
export function setIgnoredHosts(val) { ignoredHosts = val; }
export function setFocusedHosts(val) { focusedHosts = val; }
export function setFocusEnabled(val) { focusEnabled = val; }
export function setAgentPreview(val) { agentPreview = val; }
export function getAgentPreview() { return agentPreview; }
export function setAgentEnabled(val) { agentEnabled = val; }
export function setAgentExposed(val) { agentExposed = val; }
export function setLastTimestamp(val) { lastTimestamp = val; }
export function setSignatureCache(val) { signatureCache = val; }
export function setCriteriaVersion(val) { criteriaVersion = val; }
export function setTotalRequests(val) { totalRequests = val; }
export function setVisibleCount(val) { visibleCount = val; }

export let replayMode = false;
export let replayServed = new Set();
export let replayComplete = false;
export function setReplayMode(val) { replayMode = val; }
export function setReplayServed(val) { replayServed = val; }
export function setReplayComplete(val) { replayComplete = val; }
export function isReplayServed(id) { return replayServed.has(id); }
export function isReplayComplete() { return replayComplete; }
export function getReplayMode() { return replayMode; }
export function markReplayServed(id) { if (id) replayServed.add(id); }

export function applyFullList(data) {
    setRequests(data.entries);
    setTotalRequests(data.total);
    setVisibleCount(data.visibleCount);
    setCriteriaVersion(data.version);
}
export function applyPage(data) {
    for (const item of data.entries) {
        requestsMap.set(item.id, item);
    }
    requests = Array.from(requestsMap.values());
    setVisibleCount(data.visibleCount);
}
export function applyListDiff(data) {
    upsertRequests(data.upserts);
    removeRequests(data.removed || []);
    setTotalRequests(data.total);
    setVisibleCount(data.visibleCount);
    setCriteriaVersion(data.version);
}

export let rules = [];
export function setRules(val) { rules = val; }
