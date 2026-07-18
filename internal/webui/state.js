export let requests = [];
export let selectedId = null;
export let filterText = '';
export let ignoredHosts = [];

export function setRequests(val) { requests = val; }
export function setSelectedId(val) { selectedId = val; }
export function setFilterText(val) { filterText = val; }
export function setIgnoredHosts(val) { ignoredHosts = val; }
