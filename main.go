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
	"gospy/internal/webui"
)

func main() {
	proxyAddr := flag.String("addr", ":8080", "Proxy listen address")
	uiAddr := flag.String("ui", ":8081", "Web UI listen address")
	dataDir := flag.String("dir", ".gospy", "Data directory")
	noSystemProxy := flag.Bool("no-system-proxy", false, "Don't auto-configure Windows system proxy")
	resetProxy := flag.Bool("reset-proxy", false, "Restore system proxy to original settings (after crash)")
	ignoreHosts := flag.String("ignore", "", "Comma-separated hosts to ignore (e.g. \"host1.com,host2.com\")")
	focusHosts := flag.String("focus", "", "Comma-separated hosts to focus on (e.g. \"host1.com,host2.com\")")
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

	srv := proxy.NewServer(*proxyAddr, *uiAddr, caCert, hist, ruleEngine, ignoreStore, *dataDir)

	proxy.LogInfo(fmt.Sprintf("Proxy listening on %s", *proxyAddr))

	go func() {
		if err := webui.NewServer(*uiAddr, hist, ignoreStore, focusStore, rulesStore, ruleEngine, srv.Resolver(), srv.SigCache()).ListenAndServe(); err != nil {
			proxy.LogError(fmt.Sprintf("Web UI error: %v", err))
		}
	}()

	proxy.LogInfo(fmt.Sprintf("Web UI at http://localhost%s", *uiAddr))

	// System proxy auto-configuration
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
			proxy.LogInfo("⚠ Firefox detected as default browser. To proxy localhost traffic:")
			proxy.LogInfo("  1. Open about:config")
			proxy.LogInfo("  2. Set network.proxy.allow_hijacking_localhost to true")
		}
	}

	// Restore proxy on exit
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
