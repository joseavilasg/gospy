//go:build windows

package browser

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows/registry"
)

type Type int

const (
	Unknown Type = iota
	Chrome
	Edge
	Firefox
	Opera
	Brave
	Vivaldi
)

func (t Type) String() string {
	switch t {
	case Chrome:
		return "Chrome"
	case Edge:
		return "Edge"
	case Firefox:
		return "Firefox"
	case Opera:
		return "Opera"
	case Brave:
		return "Brave"
	case Vivaldi:
		return "Vivaldi"
	default:
		return "Unknown"
	}
}

func (t Type) IsChromium() bool {
	switch t {
	case Chrome, Edge, Opera, Brave, Vivaldi:
		return true
	default:
		return false
	}
}

func DetectDefault() (Type, string, error) {
	progId, err := getDefaultProgId()
	if err != nil {
		return Unknown, "", err
	}
	return classifyProgId(progId), progId, nil
}

func getDefaultProgId() (string, error) {
	key, err := registry.OpenKey(
		registry.CURRENT_USER,
		`Software\Microsoft\Windows\Shell\Associations\UrlAssociations\http\UserChoice`,
		registry.QUERY_VALUE,
	)
	if err == nil {
		defer key.Close()
		progId, _, err := key.GetStringValue("ProgId")
		if err == nil && progId != "" {
			return progId, nil
		}
	}

	key, err = registry.OpenKey(registry.CLASSES_ROOT, `http\shell\open\command`, registry.QUERY_VALUE)
	if err != nil {
		return "", fmt.Errorf("cannot determine default browser: %w", err)
	}
	defer key.Close()

	cmd, _, err := key.GetStringValue("")
	if err != nil {
		return "", fmt.Errorf("read default browser command: %w", err)
	}
	return classifyFromCommand(cmd), nil
}

func classifyProgId(progId string) Type {
	switch strings.ToLower(progId) {
	case "chromehtml":
		return Chrome
	case "msedgehtm", "msedgentm":
		return Edge
	case "firefoxurl", "firefoxhtml":
		return Firefox
	case "operastable":
		return Opera
	case "bravehtml":
		return Brave
	case "vivaldihtm":
		return Vivaldi
	default:
		return Unknown
	}
}

func classifyFromCommand(cmd string) string {
	lower := strings.ToLower(cmd)
	switch {
	case strings.Contains(lower, "chrome.exe"):
		return "ChromeHTML"
	case strings.Contains(lower, "msedge.exe"):
		return "MSEdgeHTM"
	case strings.Contains(lower, "firefox.exe"):
		return "FirefoxURL"
	case strings.Contains(lower, "opera.exe"):
		return "OperaStable"
	case strings.Contains(lower, "brave.exe"):
		return "BraveHTML"
	case strings.Contains(lower, "vivaldi.exe"):
		return "VivaldiHTM"
	default:
		return ""
	}
}
