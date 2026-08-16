package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"gospy/internal/agent"
	"gospy/internal/browser"
	"gospy/internal/ca"
	"gospy/internal/history"
	"gospy/internal/proxy"
	"gospy/internal/rules"
	"gospy/internal/session"
	"gospy/internal/webui"
)

func main() {
	mode := "normal"
	rawArgs := os.Args[1:]
	if len(rawArgs) > 0 {
		switch rawArgs[0] {
		case "record":
			mode = "record"
			os.Args = append([]string{os.Args[0]}, rawArgs[1:]...)
		case "replay":
			mode = "replay"
			os.Args = append([]string{os.Args[0]}, rawArgs[1:]...)
		}
	}

	proxyAddr := flag.String("addr", ":8080", "Proxy listen address")
	uiAddr := flag.String("ui", ":8081", "Web UI listen address")
	dataDir := flag.String("dir", ".gospy", "Data directory")
	noSystemProxy := flag.Bool("no-system-proxy", false, "Don't auto-configure Windows system proxy")
	systemProxy := flag.Bool("system-proxy", false, "Enable Windows system proxy even in record mode")
	resetProxy := flag.Bool("reset-proxy", false, "Restore system proxy to original settings (after crash)")
	ignoreHosts := flag.String("ignore", "", "Comma-separated hosts to ignore (e.g. \"host1.com,host2.com\")")
	focusHosts := flag.String("focus", "", "Comma-separated hosts to focus on (e.g. \"host1.com,host2.com\")")
	sessionDir := flag.String("session", "", "Session name or directory for recording/replay")
	matchConfig := flag.String("match-config", "", "Match configuration file for replay")
	maxDuration := flag.String("max-duration", "", "Stop recording after this duration, counted from the first captured request (e.g. 60s, 2m)")
	bindIface := flag.String("bind-iface", "", "Bind proxy outbound connections to a network interface (SO_BINDTODEVICE, Linux)")
	dnsServer := flag.String("dns", "", "Custom DNS server for proxy outbound; auto-detected from --bind-iface when empty")
	flag.Parse()
	if *uiAddr == "" {
		fmt.Fprintf(os.Stderr, "ERROR: --ui cannot be empty (the web server is always active)\n")
		os.Exit(1)
	}
	maxDur := time.Duration(0)
	if *maxDuration != "" {
		d, err := time.ParseDuration(*maxDuration)
		if err != nil || d <= 0 {
			fmt.Fprintf(os.Stderr, "ERROR: invalid --max-duration %q (use e.g. 60s, 2m)\n", *maxDuration)
			os.Exit(1)
		}
		maxDur = d
	}

	fmt.Println(`
   _____   ____    _____  _____ __     __
  / ____| / __ \  / ____||  __ \\ \   / /
 | |  __ | |  | || (___  | |__) |\ \_/ /
 | | |_ || |  | | \___ \ |  ___/  \   /
 | |__| || |__| | ____) || |       | |
  \_____| \____/ |_____/ |_|       |_|
	`)

	// --reset-proxy: restore original settings and exit
	if *resetProxy {
		backupPath := filepath.Join(*dataDir, "proxy_backup.json")
		saved, err := proxy.LoadBackup(backupPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "No backup found or error reading backup: %v\n", err)
			os.Exit(1)
		}
		if err := proxy.RestoreSystemProxy(saved); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to restore proxy: %v\n", err)
			os.Exit(1)
		}
		proxy.RemoveBackup(backupPath)
		fmt.Println("System proxy restored to original settings.")
		return
	}

	caDir := *dataDir + "/ca"
	caCert, err := ca.New(caDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing CA: %v\n", err)
		os.Exit(1)
	}

	if mode == "replay" {
		runReplay(caCert, *proxyAddr, session.ResolveDir(*sessionDir, *dataDir), *matchConfig, *uiAddr, *dataDir)
		return
	}

	fmt.Println(caCert.InstallInstructions())

	recordSessionDir := ""
	autoSession := mode == "record" && *sessionDir == ""
	var hist *history.Store
	if autoSession {
		// No session yet: the proxy rejects all traffic (503 + X-Gospy-Replay:
		// nosession) until POST /api/session/start swaps in a real store.
		hist = nil
	} else {
		histDir := *dataDir + "/history"
		if mode == "record" {
			recordSessionDir = session.ResolveDir(*sessionDir, *dataDir)
			histDir = recordSessionDir
		}
		hist, err = history.New(histDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error initializing history: %v\n", err)
			os.Exit(1)
		}
	}

	rulesStore := rules.NewStore(*dataDir + "/rules.json")
	if err := rulesStore.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "Error loading rules: %v\n", err)
		os.Exit(1)
	}

	ruleEngine := rules.NewEngine()
	ruleEngine.Load(rulesStore.GetRules())

	ignoreStore := proxy.NewIgnoreStore(*dataDir + "/ignore.json")
	if err := ignoreStore.Load(); err != nil {
		proxy.LogError(fmt.Sprintf("Failed to load ignore list: %v", err))
	}
	if *ignoreHosts != "" {
		for h := range strings.SplitSeq(*ignoreHosts, ",") {
			h = strings.TrimSpace(h)
			if h != "" {
				if err := ignoreStore.Add(h); err != nil {
					proxy.LogError(fmt.Sprintf("Failed to ignore host %s: %v", h, err))
				}
			}
		}
	}

	focusStore := proxy.NewFocusStore(*dataDir + "/focus.json")
	if err := focusStore.Load(); err != nil {
		proxy.LogError(fmt.Sprintf("Failed to load focus list: %v", err))
	}
	if *focusHosts != "" {
		for h := range strings.SplitSeq(*focusHosts, ",") {
			h = strings.TrimSpace(h)
			if h != "" {
				if err := focusStore.Add(h); err != nil {
					proxy.LogError(fmt.Sprintf("Failed to focus host %s: %v", h, err))
				}
			}
		}
	}

	if mode == "record" && recordSessionDir != "" {
		proxy.LogInfo(fmt.Sprintf("Recording session to %s", recordSessionDir))
	}
	if autoSession {
		proxy.LogInfo("Waiting for session start: POST http://localhost:8081/api/session/start")
		proxy.LogInfo("Proxy traffic is rejected until a session starts")
	}
	if mode == "record" && !*systemProxy {
		*noSystemProxy = true
	}

	filterStore := webui.NewFilterStore(*dataDir + "/filters.json")
	if err := filterStore.Load(); err != nil {
		proxy.LogError(fmt.Sprintf("Failed to load filters: %v", err))
	}

	srv, err := proxy.NewServer(*proxyAddr, *uiAddr, caCert, hist, ruleEngine, ignoreStore, *dataDir, *bindIface, *dnsServer)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting proxy: %v\n", err)
		os.Exit(1)
	}

	proxy.LogInfo(fmt.Sprintf("Proxy listening on %s", *proxyAddr))

	webSrv := webui.NewServer(*uiAddr, hist, ignoreStore, focusStore, rulesStore, ruleEngine, *proxyAddr, srv.Resolver(), srv.SigCache(), filterStore)
	srv.SetStreamNotifier(webSrv.StreamNotifier())
	if mode == "record" && recordSessionDir != "" && maxDur > 0 {
		wireMaxDuration(hist, srv, webSrv, maxDur, *maxDuration)
	}

	var mcpServer *agent.Server
	if autoSession {
		sessionMgr := session.NewManager(filepath.Join(*dataDir, "sessions"))
		webSrv.SetSessionStarter(func(name string) (string, string, error) {
			dir, store, err := sessionMgr.Start(name)
			if err != nil {
				return "", "", err
			}
			srv.SetHistoryStore(store)
			webSrv.SetHistoryStore(store)
			if mcpServer != nil {
				mcpServer.SetHistoryStore(store)
			}
			if maxDur > 0 {
				wireMaxDuration(store, srv, webSrv, maxDur, *maxDuration)
			}
			proxy.LogInfo(fmt.Sprintf("Recording session started: %s", dir))
			return dir, filepath.Base(dir), nil
		})
	}

	go func() {
		if err := webSrv.ListenAndServe(); err != nil {
			proxy.LogError(fmt.Sprintf("Web UI error: %v", err))
		}
	}()

	proxy.LogInfo(fmt.Sprintf("Web UI at http://localhost%s", *uiAddr))

	agentFwd, err := agent.NewForwarder("http://127.0.0.1"+*proxyAddr, caCert.TLSCert())
	if err != nil {
		proxy.LogError(fmt.Sprintf("Agent forwarder error: %v", err))
	} else {
		mcpServer = agent.NewServer(agent.NewScope(hist, filterStore, ignoreStore, focusStore), hist, agentFwd)
		mux := http.NewServeMux()
		mux.Handle("/mcp", mcpServer.Handler())
		go func() {
			proxy.LogInfo("Agent MCP at http://127.0.0.1:8090/mcp")
			if err := http.ListenAndServe("127.0.0.1:8090", mux); err != nil {
				proxy.LogError(fmt.Sprintf("Agent MCP error: %v", err))
			}
		}()
	}

	var savedProxy *proxy.SavedProxy
	backupPath := filepath.Join(*dataDir, "proxy_backup.json")
	if !*noSystemProxy {
		saved, err := proxy.GetSystemProxy()
		if err != nil {
			proxy.LogError(fmt.Sprintf("Failed to read proxy settings: %v", err))
		} else {
			savedProxy = saved
			if err := proxy.SaveBackup(saved, backupPath); err != nil {
				proxy.LogError(fmt.Sprintf("Failed to save proxy backup: %v", err))
			}
			listenAddr := "127.0.0.1" + *proxyAddr
			if err := proxy.SetSystemProxy(listenAddr); err != nil {
				proxy.LogError(fmt.Sprintf("Failed to set system proxy: %v", err))
			} else {
				proxy.LogInfo(fmt.Sprintf("System proxy enabled → %s", listenAddr))
			}
		}
	}

	if !*noSystemProxy {
		browserType, _, err := browser.DetectDefault()
		if err == nil && browserType == browser.Firefox {
			proxy.LogInfo("? Firefox detected as default browser. To proxy localhost traffic:")
			proxy.LogInfo("  1. Open about:config")
			proxy.LogInfo("  2. Set network.proxy.allow_hijacking_localhost to true")
		}
	}

	cleanup := func() {
		if savedProxy != nil {
			proxy.LogInfo("Restoring original proxy settings...")
			if err := proxy.RestoreSystemProxy(savedProxy); err != nil {
				proxy.LogError(fmt.Sprintf("Failed to restore proxy: %v", err))
			}
			proxy.RemoveBackup(backupPath)
		}
	}

	defer cleanup()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println()
		proxy.LogInfo("Shutting down...")
		cleanup()
		os.Exit(0)
	}()

	proxy.LogInfo("Press Ctrl+C to stop")

	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "Proxy error: %v\n", err)
		os.Exit(1)
	}
}

func runReplay(caCert *ca.CA, addr, sessionDir, matchConfig, uiAddr, dataDir string) {
	if sessionDir == "" {
		fmt.Fprintf(os.Stderr, "ERROR: --session is required for replay mode\n")
		os.Exit(1)
	}
	if _, err := os.Stat(sessionDir); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "ERROR: session %s not found\n", sessionDir)
		os.Exit(1)
	}

	hist, err := history.New(sessionDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: failed to load session: %v\n", err)
		os.Exit(1)
	}

	var cfg *session.MatchConfig
	if matchConfig != "" {
		cfg, err = session.LoadMatchConfig(matchConfig)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: failed to load match config: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Loaded match config: %d ignored params\n",
			len(cfg.IgnoreQueryParams))
	}

	srv := session.NewReplayServer(addr, caCert, session.NewReplayStore(hist), cfg)
	fmt.Printf("Replay server listening on %s\n", addr)
	fmt.Println("WARNING: All requests are served from recording, no network calls will be made")

	resolver := proxy.NewClientResolver(addr)
	defer resolver.Stop()
	srv.SetOriginResolver(func(remoteAddr string) *session.ClientOrigin {
		pi := resolver.Resolve(remoteAddr)
		if pi == nil {
			return nil
		}
		return &session.ClientOrigin{Name: pi.Name, Path: pi.Path, PID: pi.PID}
	})

	replayRoot := filepath.Join(dataDir, "replay", filepath.Base(sessionDir))
	srv.SetReplayLogRoot(replayRoot)

	uiBase, err := os.MkdirTemp("", "gospy-replay-ui-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: failed to create UI temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(uiBase)

	ignoreStore := proxy.NewIgnoreStore(filepath.Join(uiBase, "ignore.json"))
	_ = ignoreStore.Load()
	focusStore := proxy.NewFocusStore(filepath.Join(uiBase, "focus.json"))
	_ = focusStore.Load()
	rulesStore := rules.NewStore(filepath.Join(replayRoot, "rules.json"))
	if err := rulesStore.Load(); err != nil {
		proxy.LogError(fmt.Sprintf("Failed to load replay rules: %v", err))
	}
	ruleEngine := rules.NewEngine()
	ruleEngine.Load(rulesStore.GetRules())
	filterStore := webui.NewFilterStore(filepath.Join(uiBase, "filters.json"))

	sigCache := proxy.NewSignatureCache(filepath.Join(uiBase, "signatures"))
	webSrv := webui.NewServer(uiAddr, hist, ignoreStore, focusStore, rulesStore, ruleEngine, addr, nil, sigCache, filterStore)
	webSrv.SetReplayMode(true)
	webSrv.SetReplayLogDir(replayRoot)
	srv.SetReplayNotifier(webSrv.ReplayNotifier())
	srv.SetRulesEngine(ruleEngine)

	go func() {
		if err := webSrv.ListenAndServe(); err != nil {
			proxy.LogError(fmt.Sprintf("Web UI error: %v", err))
		}
	}()

	proxy.LogInfo(fmt.Sprintf("Web UI at http://localhost%s", uiAddr))
	proxy.LogInfo("Replay mode: read-only")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println()
		fmt.Println("Shutting down...")
		if err := srv.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Error finalizing replay log: %v\n", err)
		}
		os.Exit(0)
	}()

	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "Replay server error: %v\n", err)
		os.Exit(1)
	}
}

// wireMaxDuration stops recording maxDur after the session's first captured
// request. Each session store gets its own window (rotating to a new session
// arms a fresh timer); the expiry callback only pauses the capture if that
// session is still the active one.
func wireMaxDuration(store *history.Store, srv *proxy.Server, webSrv *webui.Server, maxDur time.Duration, maxLabel string) {
	var once sync.Once
	store.OnSave(func(*history.Entry) {
		once.Do(func() {
			time.AfterFunc(maxDur, func() {
				if srv.CaptureStore() != store {
					return
				}
				srv.SetCaptureStopped(true)
				webSrv.SetRecordingStopped(maxLabel)
				proxy.LogInfo(fmt.Sprintf("Recording stopped after %s (max-duration), session %s", maxLabel, filepath.Base(store.Dir())))
			})
		})
	})
}
