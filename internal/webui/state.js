export let requests = [];
export let selectedId = null;
export let filterText = '';
export let ignoredHosts = [];
export let focusedHosts = [];
export let focusEnabled = localStorage.getItem('gospy-focus-enabled') === 'true';
export let lastTimestamp = '';

export function setRequests(val) { requests = val; }
export function setSelectedId(val) { selectedId = val; }
export function setFilterText(val) { filterText = val; }
export function setIgnoredHosts(val) { ignoredHosts = val; }
export function setFocusedHosts(val) { focusedHosts = val; }
export function setFocusEnabled(val) {
    focusEnabled = val;
    localStorage.setItem('gospy-focus-enabled', val);
}
export function setLastTimestamp(val) { lastTimestamp = val; }
