package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type LogLevel int

const (
	LevelError LogLevel = iota
	LevelWarn
	LevelInfo
	LevelDebug
	LevelTrace
)

func parseLogLevel(s string) LogLevel {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "ERROR":
		return LevelError
	case "WARN", "WARNING":
		return LevelWarn
	case "DEBUG":
		return LevelDebug
	case "TRACE":
		return LevelTrace
	default:
		return LevelInfo
	}
}
func (l LogLevel) String() string {
	switch l {
	case LevelError:
		return "ERROR"
	case LevelWarn:
		return "WARN"
	case LevelDebug:
		return "DEBUG"
	case LevelTrace:
		return "TRACE"
	default:
		return "INFO"
	}
}

type LogEntry struct {
	Time      string         `json:"time"`
	Level     string         `json:"level"`
	Component string         `json:"component"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
}

type AppLogger struct {
	mu          sync.Mutex
	dir         string
	level       LogLevel
	currentDate string
	file        *os.File
	ring        []LogEntry
	ringMax     int
	stderr      io.Writer
}

func NewAppLogger(dir, level string) (*AppLogger, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	l := &AppLogger{dir: dir, level: parseLogLevel(level), ringMax: 3000, stderr: os.Stderr}
	l.cleanupOldFiles(10)
	if err := l.rotateLocked(time.Now()); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *AppLogger) SetLevel(level string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = parseLogLevel(level)
	return l.level.String()
}
func (l *AppLogger) Level() string { l.mu.Lock(); defer l.mu.Unlock(); return l.level.String() }
func (l *AppLogger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
}

func (l *AppLogger) cleanupOldFiles(keep int) {
	matches, _ := filepath.Glob(filepath.Join(l.dir, "sds200-*.log"))
	sort.Slice(matches, func(i, j int) bool {
		ai, _ := os.Stat(matches[i])
		aj, _ := os.Stat(matches[j])
		if ai == nil || aj == nil {
			return matches[i] < matches[j]
		}
		return ai.ModTime().After(aj.ModTime())
	})
	for i := keep; i < len(matches); i++ {
		_ = os.Remove(matches[i])
	}
}
func (l *AppLogger) rotateLocked(now time.Time) error {
	date := now.Format("2006-01-02")
	if l.file != nil && date == l.currentDate {
		if st, err := l.file.Stat(); err == nil && st.Size() < 8*1024*1024 {
			return nil
		}
		_ = l.file.Close()
		l.file = nil
		// Size rotation within the same day.
		base := filepath.Join(l.dir, "sds200-"+date+".log")
		for i := 4; i >= 1; i-- {
			old := fmt.Sprintf("%s.%d", base, i)
			next := fmt.Sprintf("%s.%d", base, i+1)
			if _, err := os.Stat(old); err == nil {
				_ = os.Rename(old, next)
			}
		}
		if _, err := os.Stat(base); err == nil {
			_ = os.Rename(base, base+".1")
		}
	} else if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	path := filepath.Join(l.dir, "sds200-"+date+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	l.file = f
	l.currentDate = date
	return nil
}

func (l *AppLogger) Log(level LogLevel, component, message string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if level > l.level {
		return
	}
	now := time.Now()
	_ = l.rotateLocked(now)
	component = strings.ToUpper(strings.TrimSpace(component))
	if component == "" {
		component = "APP"
	}
	message = strings.ReplaceAll(message, "\r", "\\r")
	message = strings.ReplaceAll(message, "\n", "\\n")
	e := LogEntry{Time: now.Format("2006-01-02 15:04:05.000"), Level: level.String(), Component: component, Message: message, Fields: fields}
	l.ring = append(l.ring, e)
	if len(l.ring) > l.ringMax {
		l.ring = append([]LogEntry(nil), l.ring[len(l.ring)-l.ringMax:]...)
	}
	fieldText := ""
	if len(fields) > 0 {
		if b, err := json.Marshal(fields); err == nil {
			fieldText = " " + string(b)
		}
	}
	line := fmt.Sprintf("%s %-5s %-10s %s%s\n", e.Time, e.Level, e.Component, e.Message, fieldText)
	if l.file != nil {
		_, _ = l.file.WriteString(line)
		_ = l.file.Sync()
	}
	if level <= LevelWarn || l.level >= LevelDebug {
		_, _ = io.WriteString(l.stderr, line)
	}
}
func (l *AppLogger) Error(c, m string, f map[string]any) { l.Log(LevelError, c, m, f) }
func (l *AppLogger) Warn(c, m string, f map[string]any)  { l.Log(LevelWarn, c, m, f) }
func (l *AppLogger) Info(c, m string, f map[string]any)  { l.Log(LevelInfo, c, m, f) }
func (l *AppLogger) Debug(c, m string, f map[string]any) { l.Log(LevelDebug, c, m, f) }
func (l *AppLogger) Trace(c, m string, f map[string]any) { l.Log(LevelTrace, c, m, f) }

func (l *AppLogger) Entries(limit int, level, component, q string) []LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	level = strings.ToUpper(level)
	component = strings.ToUpper(component)
	q = strings.ToLower(q)
	out := make([]LogEntry, 0, limit)
	for i := len(l.ring) - 1; i >= 0 && len(out) < limit; i-- {
		e := l.ring[i]
		if level != "" && e.Level != level {
			continue
		}
		if component != "" && e.Component != component {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(e.Message+" "+e.Component), q) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func (l *AppLogger) CurrentPath() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.currentDate == "" {
		return ""
	}
	return filepath.Join(l.dir, "sds200-"+l.currentDate+".log")
}
func (l *AppLogger) TailText(lines int) string {
	path := l.CurrentPath()
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if lines <= 0 {
		lines = 200
	}
	sc := bufio.NewScanner(f)
	all := make([]string, 0, lines+1)
	for sc.Scan() {
		all = append(all, sc.Text())
		if len(all) > lines {
			all = all[1:]
		}
	}
	return strings.Join(all, "\n") + "\n"
}
