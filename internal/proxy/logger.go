package proxy

import (
	"fmt"
	"time"
)

const (
	colorReset   = "\033[0m"
	colorRed     = "\033[31m"
	colorGreen   = "\033[32m"
	colorYellow  = "\033[33m"
	colorBlue    = "\033[34m"
	colorMagenta = "\033[35m"
	colorCyan    = "\033[36m"
	colorGray    = "\033[90m"
	colorBold    = "\033[1m"
)

func colorMethod(method string) string {
	switch method {
	case "GET":
		return colorGreen + method + colorReset
	case "POST":
		return colorYellow + method + colorReset
	case "PUT":
		return colorBlue + method + colorReset
	case "DELETE":
		return colorRed + method + colorReset
	case "PATCH":
		return colorMagenta + method + colorReset
	case "CONNECT":
		return colorCyan + method + colorReset
	default:
		return colorGray + method + colorReset
	}
}

func colorStatus(code int) string {
	switch {
	case code >= 200 && code < 300:
		return colorGreen + fmt.Sprintf("%d", code) + colorReset
	case code >= 300 && code < 400:
		return colorYellow + fmt.Sprintf("%d", code) + colorReset
	case code >= 400 && code < 500:
		return colorRed + fmt.Sprintf("%d", code) + colorReset
	case code >= 500:
		return colorRed + colorBold + fmt.Sprintf("%d", code) + colorReset
	default:
		return fmt.Sprintf("%d", code)
	}
}

func LogRequest(method, url string) {
	ts := time.Now().Format("15:04:05.000")
	fmt.Printf("%s %s %s%s%s → %s\n",
		colorGray+ts+colorReset,
		colorBold+"REQ"+colorReset,
		colorMethod(method),
		colorReset,
		" ",
		url,
	)
}

func LogResponse(method, url string, status int, contentType string) {
	ts := time.Now().Format("15:04:05.000")
	ct := contentType
	if ct == "" {
		ct = "-"
	}
	fmt.Printf("%s %s %s %s%s%s %s %s\n",
		colorGray+ts+colorReset,
		colorBold+"RES"+colorReset,
		colorStatus(status),
		colorMethod(method),
		colorReset,
		" ",
		url,
		colorGray+ct+colorReset,
	)
}

func LogConnect(host string) {
	ts := time.Now().Format("15:04:05.000")
	fmt.Printf("%s %s %s → %s\n",
		colorGray+ts+colorReset,
		colorCyan+"CON"+colorReset,
		" ",
		host,
	)
}

func LogMITM(host string) {
	ts := time.Now().Format("15:04:05.000")
	fmt.Printf("%s %s %s%s\n",
		colorGray+ts+colorReset,
		colorMagenta+"MITM"+colorReset,
		" ",
		colorCyan+host+colorReset,
	)
}

func LogInfo(msg string) {
	ts := time.Now().Format("15:04:05.000")
	fmt.Printf("%s %s %s\n",
		colorGray+ts+colorReset,
		colorBlue+"INFO"+colorReset,
		msg,
	)
}

func LogError(msg string) {
	ts := time.Now().Format("15:04:05.000")
	fmt.Printf("%s %s %s\n",
		colorGray+ts+colorReset,
		colorRed+"ERR"+colorReset,
		msg,
	)
}
