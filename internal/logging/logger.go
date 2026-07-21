package logging

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

type ConsoleConfig struct {
	Level      zapcore.Level
	TimeFormat string
}

type FileConfig struct {
	Path       string
	Level      zapcore.Level
	MaxSizeMB  int
	MaxBackups int
	MaxAgeDays int
	Compress   bool
}

var console = ConsoleConfig{
	Level:      zapcore.DebugLevel,
	TimeFormat: "15:04:05.000",
}

var file = FileConfig{
	Path:       ".gospy/logs/gospy.log",
	Level:      zapcore.InfoLevel,
	MaxSizeMB:  10,
	MaxBackups: 3,
	MaxAgeDays: 15,
	Compress:   true,
}

var (
	Log  *zap.SugaredLogger
	once sync.Once
)

func init() {
	once.Do(func() {
		Log = newLogger().Sugar()
	})
}

func newLogger() *zap.Logger {
	var cores []zapcore.Core

	cores = append(cores, &prettyCore{
		level: console.Level,
		out:   zapcore.Lock(os.Stdout),
	})

	if file.Path != "" {
		lj := &lumberjack.Logger{
			Filename:   file.Path,
			MaxSize:    file.MaxSizeMB,
			MaxBackups: file.MaxBackups,
			MaxAge:     file.MaxAgeDays,
			Compress:   file.Compress,
		}
		encoderCfg := zap.NewProductionEncoderConfig()
		encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder
		encoderCfg.EncodeLevel = zapcore.CapitalLevelEncoder
		fileCore := zapcore.NewCore(
			zapcore.NewJSONEncoder(encoderCfg),
			zapcore.AddSync(lj),
			zap.NewAtomicLevelAt(file.Level),
		)
		cores = append(cores, &componentFilterCore{core: fileCore})
	}

	return zap.New(zapcore.NewTee(cores...), zap.AddStacktrace(zapcore.ErrorLevel))
}

func Component(name string) *zap.SugaredLogger {
	return Log.With(zap.String("component", name))
}

// componentFilterCore suppresses logs that carry a "component" field.
// This prevents REQ/RES/CON/MITM/IGN from being written to the file.
type componentFilterCore struct {
	core         zapcore.Core
	hasComponent bool
}

func (c *componentFilterCore) Enabled(_ zapcore.Level) bool {
	return !c.hasComponent
}

func (c *componentFilterCore) With(fields []zapcore.Field) zapcore.Core {
	hasComp := c.hasComponent
	for _, f := range fields {
		if f.Key == "component" {
			hasComp = true
		}
	}
	return &componentFilterCore{core: c.core.With(fields), hasComponent: hasComp}
}

func (c *componentFilterCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.hasComponent {
		return ce
	}
	return c.core.Check(ent, ce)
}

func (c *componentFilterCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	if c.hasComponent {
		return nil
	}
	return c.core.Write(ent, fields)
}

func (c *componentFilterCore) Sync() error { return c.core.Sync() }

// prettyCore formats logs in a compact, colored, human-readable format.
// Format: HH:MM:SS.mmm TAG [id] METHOD → URL   (for component logs)
// Format: HH:MM:SS.mmm LEVEL message  key=val   (for general logs)
type prettyCore struct {
	level  zapcore.Level
	out    zapcore.WriteSyncer
	fields []zapcore.Field
}

func (c *prettyCore) Enabled(_ zapcore.Level) bool { return true }

func (c *prettyCore) With(fields []zapcore.Field) zapcore.Core {
	return &prettyCore{
		level:  c.level,
		out:    c.out,
		fields: append(c.fields[:len(c.fields):len(c.fields)], fields...),
	}
}

func (c *prettyCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if ent.Level >= c.level {
		return ce.AddCore(ent, c)
	}
	return ce
}

func (c *prettyCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	allFields := append(c.fields[:len(c.fields):len(c.fields)], fields...)

	var component string
	kv := make(map[string]interface{})
	for _, f := range allFields {
		if f.Key == "component" {
			component = f.String
		} else {
			kv[f.Key] = unwrapField(f)
		}
	}

	ts := ent.Time.Format(console.TimeFormat)

	if component != "" {
		_, err := c.out.Write([]byte(c.formatComponent(ts, component, kv)))
		return err
	}
	_, err := c.out.Write([]byte(c.formatGeneral(ts, ent.Level, ent.Message, kv)))
	return err
}

func (c *prettyCore) Sync() error { return c.out.Sync() }

func (c *prettyCore) formatComponent(ts, component string, kv map[string]interface{}) string {
	id, _ := kv["id"].(string)

	switch component {
	case "req":
		method, _ := kv["method"].(string)
		url, _ := kv["url"].(string)
		return fmt.Sprintf("%s %s %s %s%s%s → %s\n",
			colorGray+ts+colorReset,
			colorBold+"REQ"+colorReset,
			colorGray+"["+id+"]"+colorReset,
			colorMethod(method), colorReset, " ", url,
		)

	case "res":
		method, _ := kv["method"].(string)
		url, _ := kv["url"].(string)
		ct, _ := kv["content_type"].(string)
		status := toInt(kv["status"])
		if ct == "" {
			ct = "-"
		}
		return fmt.Sprintf("%s %s %s %s %s%s%s %s %s\n",
			colorGray+ts+colorReset,
			colorBold+"RES"+colorReset,
			colorGray+"["+id+"]"+colorReset,
			colorStatus(status),
			colorMethod(method), colorReset, " ", url,
			colorGray+ct+colorReset,
		)

	case "con":
		host, _ := kv["host"].(string)
		return fmt.Sprintf("%s %s %s → %s\n",
			colorGray+ts+colorReset,
			colorCyan+"CON"+colorReset,
			" ", host,
		)

	case "mitm":
		host, _ := kv["host"].(string)
		return fmt.Sprintf("%s %s %s%s\n",
			colorGray+ts+colorReset,
			colorMagenta+"MITM"+colorReset,
			" ", colorCyan+host+colorReset,
		)

	case "ign":
		method, _ := kv["method"].(string)
		url, _ := kv["url"].(string)
		return fmt.Sprintf("%s %s %s %s %s\n",
			colorGray+ts+colorReset,
			colorGray+"IGN"+colorReset,
			colorGray+method+colorReset,
			colorGray+url+colorReset, "",
		)
	}

	return fmt.Sprintf("%s %s [%s]\n", colorGray+ts+colorReset, component, fmt.Sprint(kv))
}

func (c *prettyCore) formatGeneral(ts string, level zapcore.Level, msg string, kv map[string]interface{}) string {
	icon, color := levelStyle(level)

	line := fmt.Sprintf("%s  %s %s%-5s%s  %s",
		ts, icon, color, strings.ToUpper(level.String()), colorReset, msg,
	)

	if len(kv) > 0 {
		parts := make([]string, 0, len(kv))
		for k, v := range kv {
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
		line += "  " + colorGray + strings.Join(parts, " ") + colorReset
	}
	line += "\n"
	return line
}

func levelStyle(l zapcore.Level) (string, string) {
	switch l {
	case zapcore.DebugLevel:
		return "🔍", colorMagenta
	case zapcore.InfoLevel:
		return "✓ ", colorGreen
	case zapcore.WarnLevel:
		return "⚠ ", colorYellow
	case zapcore.ErrorLevel, zapcore.DPanicLevel, zapcore.PanicLevel, zapcore.FatalLevel:
		return "✗ ", colorRed
	default:
		return "· ", colorGray
	}
}

func unwrapField(f zapcore.Field) interface{} {
	switch f.Type {
	case zapcore.StringType:
		return f.String
	case zapcore.Int64Type, zapcore.Int32Type:
		return f.Integer
	case zapcore.ErrorType:
		if f.Interface != nil {
			return f.Interface.(error).Error()
		}
		return ""
	case zapcore.BoolType:
		return f.Integer != 0
	case zapcore.DurationType:
		d := time.Duration(f.Integer)
		return d.String()
	default:
		if f.Interface != nil {
			return f.Interface
		}
		return f.Integer
	}
}

func toInt(v interface{}) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}

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
