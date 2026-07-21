package proxy

import (
	"gospy/internal/logging"

	"go.uber.org/zap"
)

func LogRequest(id, method, url string) {
	logging.Component("req").Infow("",
		zap.String("id", id),
		zap.String("method", method),
		zap.String("url", url),
	)
}

func LogResponse(id, method, url string, status int, contentType string) {
	logging.Component("res").Infow("",
		zap.String("id", id),
		zap.String("method", method),
		zap.String("url", url),
		zap.Int("status", status),
		zap.String("content_type", contentType),
	)
}

func LogConnect(host string) {
	logging.Component("con").Infow("",
		zap.String("host", host),
	)
}

func LogMITM(host string) {
	logging.Component("mitm").Infow("",
		zap.String("host", host),
	)
}

func LogInfo(msg string) {
	logging.Log.Info(msg)
}

func LogError(msg string) {
	logging.Log.Error(msg)
}

func LogIgnored(method, url string) {
	logging.Component("ign").Infow("",
		zap.String("method", method),
		zap.String("url", url),
	)
}
