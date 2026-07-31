let requestsMap = new Map();
export let requests = [];
export let selectedId = null;
export let filterText = '';
export let ignoredHosts = [];
export let focusedHosts = [];
export let focusEnabled = false;
export let agentView = false;
export let lastTimestamp = '';
export let signatureCache = {};
export let criteriaVersion = null;
export let totalRequests = 0;

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
export function setAgentView(val) { agentView = val; }
export function getAgentView() { return agentView; }
export function setLastTimestamp(val) { lastTimestamp = val; }
export function setSignatureCache(val) { signatureCache = val; }
export function setCriteriaVersion(val) { criteriaVersion = val; }
export function setTotalRequests(val) { totalRequests = val; }

export function applyFullList(data) {
    setRequests(data.entries);
    setTotalRequests(data.total);
    setCriteriaVersion(data.version);
}
export function applyListDiff(data) {
    upsertRequests(data.upserts);
    removeRequests(data.removed || []);
    setTotalRequests(data.total);
    setCriteriaVersion(data.version);
}

export let rules = [];
export function setRules(val) { rules = val; }
