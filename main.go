package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

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
	resetProxy := flag.Bool("reset-proxy", false, "Restore system proxy to original settings (after crash)")
	ignoreHosts := flag.String("ignore", "", "Comma-separated hosts to ignore (e.g. \"host1.com,host2.com\")")
	focusHosts := flag.String("focus", "", "Comma-separated hosts to focus on (e.g. \"host1.com,host2.com\")")
	sessionDir := flag.String("session", "", "Session directory for recording/replay")
	matchConfig := flag.String("match-config", "", "Match configuration file for replay")
	flag.Parse()

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
		runReplay(caCert, *proxyAddr, *sessionDir, *matchConfig)
		return
	}

	fmt.Println(caCert.InstallInstructions())

	hist, err := history.New(*dataDir + "/history")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing history: %v\n", err)
		os.Exit(1)
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

	if mode == "record" && *sessionDir != "" {
		rec := session.NewRecorder(*dataDir+"/history", *sessionDir)
		rec.Subscribe(hist)
		proxy.LogInfo(fmt.Sprintf("Recording session to %s", *sessionDir))
	}

	srv := proxy.NewServer(*proxyAddr, *uiAddr, caCert, hist, ruleEngine, ignoreStore, *dataDir)

	proxy.LogInfo(fmt.Sprintf("Proxy listening on %s", *proxyAddr))

	go func() {
		if err := webui.NewServer(*uiAddr, hist, ignoreStore, focusStore, rulesStore, ruleEngine, *proxyAddr, srv.Resolver(), srv.SigCache()).ListenAndServe(); err != nil {
			proxy.LogError(fmt.Sprintf("Web UI error: %v", err))
		}
	}()

	proxy.LogInfo(fmt.Sprintf("Web UI at http://localhost%s", *uiAddr))

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

func runReplay(caCert *ca.CA, addr, sessionDir, matchConfig string) {
	if sessionDir == "" {
		fmt.Fprintf(os.Stderr, "ERROR: --session is required for replay mode\n")
		os.Exit(1)
	}

	s, err := session.NewOrLoad(sessionDir)
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
		fmt.Printf("Loaded match config: %d ignored params, %d ignored headers\n",
			len(cfg.IgnoreQueryParams), len(cfg.IgnoreHeaders))
	}

	srv := session.NewReplayServer(addr, caCert, s, cfg)
	fmt.Printf("Replay server listening on %s\n", addr)
	fmt.Println("WARNING: All requests are served from recording, no network calls will be made")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println()
		fmt.Println("Shutting down...")
		os.Exit(0)
	}()

	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintf(os.Stderr, "Replay server error: %v\n", err)
		os.Exit(1)
	}
}
