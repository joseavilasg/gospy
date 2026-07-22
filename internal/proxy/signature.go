package proxy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type SignatureResult struct {
	FilePath   string    `json:"filePath"`
	IsSigned   bool      `json:"isSigned"`
	SignerName string    `json:"signerName,omitempty"`
	VerifiedAt time.Time `json:"verifiedAt"`
	Error      string    `json:"error,omitempty"`
}

type SignatureCache struct {
	mu       sync.RWMutex
	cache    map[string]*SignatureResult
	dataDir  string
	onUpdate func(result *SignatureResult)
}

func NewSignatureCache(dataDir string) *SignatureCache {
	sc := &SignatureCache{
		cache:   make(map[string]*SignatureResult),
		dataDir: dataDir,
	}
	sc.load()
	return sc
}

func (sc *SignatureCache) OnUpdate(fn func(*SignatureResult)) {
	sc.onUpdate = fn
}

func (sc *SignatureCache) Get(filePath string) *SignatureResult {
	sc.mu.RLock()
	result, ok := sc.cache[filePath]
	sc.mu.RUnlock()
	if ok {
		return result
	}
	return nil
}

func (sc *SignatureCache) VerifyAsync(filePath string) {
	if sc.Get(filePath) != nil {
		return
	}

	sc.mu.Lock()
	sc.cache[filePath] = &SignatureResult{
		FilePath:   filePath,
		VerifiedAt: time.Now(),
	}
	sc.mu.Unlock()

	go func() {
		result := verifyFile(filePath)
		sc.mu.Lock()
		sc.cache[filePath] = result
		sc.mu.Unlock()

		sc.save()
		if sc.onUpdate != nil {
			sc.onUpdate(result)
		}
	}()
}

func (sc *SignatureCache) load() {
	path := filepath.Join(sc.dataDir, "signatures.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var cache map[string]*SignatureResult
	if err := json.Unmarshal(data, &cache); err != nil {
		return
	}

	sc.mu.Lock()
	sc.cache = cache
	sc.mu.Unlock()
}

func (sc *SignatureCache) save() {
	sc.mu.RLock()
	data, err := json.MarshalIndent(sc.cache, "", "  ")
	sc.mu.RUnlock()
	if err != nil {
		return
	}

	path := filepath.Join(sc.dataDir, "signatures.json")
	os.MkdirAll(sc.dataDir, 0755)
	os.WriteFile(path, data, 0644)
}
