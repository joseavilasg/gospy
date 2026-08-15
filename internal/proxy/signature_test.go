package proxy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSignatureCacheSnapshotSkipsInFlight(t *testing.T) {
	sc := NewSignatureCache(t.TempDir())

	sc.mu.Lock()
	sc.cache["/bin/inflight"] = &SignatureResult{FilePath: "/bin/inflight", InFlight: true}
	sc.cache["/bin/done"] = &SignatureResult{FilePath: "/bin/done", IsSigned: true}
	sc.mu.Unlock()

	got := sc.Snapshot()
	if len(got) != 1 || got[0].FilePath != "/bin/done" {
		t.Fatalf("Snapshot: expected only the completed result, got %+v", got)
	}
}

func TestSignatureCacheSaveSkipsInFlight(t *testing.T) {
	dir := t.TempDir()
	sc := NewSignatureCache(dir)

	sc.mu.Lock()
	sc.cache["/bin/inflight"] = &SignatureResult{FilePath: "/bin/inflight", InFlight: true}
	sc.cache["/bin/done"] = &SignatureResult{FilePath: "/bin/done", IsSigned: true}
	sc.mu.Unlock()

	sc.save()

	data, err := os.ReadFile(filepath.Join(dir, "signatures.json"))
	if err != nil {
		t.Fatalf("reading signatures.json: %v", err)
	}
	var persisted map[string]*SignatureResult
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("parsing signatures.json: %v", err)
	}
	if _, ok := persisted["/bin/inflight"]; ok {
		t.Fatal("save: an in-flight placeholder must not be persisted")
	}
	if _, ok := persisted["/bin/done"]; !ok {
		t.Fatal("save: completed results must be persisted")
	}
}
