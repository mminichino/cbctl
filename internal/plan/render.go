package plan

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mminichino/cbctl/internal/redact"
)

// FormatDuration renders a duration like capellactl: 1.2s under 10 s when fractional, else whole
// seconds as 12s, 4m0s, 1h2m3s.
func FormatDuration(d time.Duration) string {
	seconds := d.Seconds()
	if seconds < 10 && seconds != math.Trunc(seconds) {
		return fmt.Sprintf("%.1fs", seconds)
	}
	s := int64(math.RoundToEven(seconds))
	h, rem := s/3600, s%3600
	m, sec := rem/60, rem%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%dm%ds", h, m, sec)
	case m > 0:
		return fmt.Sprintf("%dm%ds", m, sec)
	}
	return fmt.Sprintf("%ds", sec)
}

// FmtValue formats a change value: nil is "(none)", bools true/false, strings quoted, lists
// "[a, b]" with element quotes stripped, mappings as key-sorted JSON.
func FmtValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "(none)"
	case bool:
		return strconv.FormatBool(x)
	case string:
		return `"` + x + `"`
	}
	if list, ok := AsList(v); ok {
		parts := make([]string, len(list))
		for i, e := range list {
			parts[i] = strings.Trim(FmtValue(e), `"`)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	if m, ok := AsObj(v); ok {
		return pyJSON(m)
	}
	return fmtNumber(v)
}

func fmtNumber(v any) string {
	var f float64
	switch x := v.(type) {
	case float64:
		f = x
	case float32:
		f = float64(x)
	default:
		if i, ok := AsInt(v); ok {
			return strconv.FormatInt(i, 10)
		}
		return fmt.Sprint(v)
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e16 {
		return strconv.FormatFloat(f, 'f', 1, 64)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// pyJSON mirrors Python's json.dumps(sort_keys=True): ", " and ": " separators, ASCII-only strings.
func pyJSON(v any) string {
	var sb strings.Builder
	writePyJSON(&sb, v)
	return sb.String()
}

func writePyJSON(sb *strings.Builder, v any) {
	switch x := v.(type) {
	case nil:
		sb.WriteString("null")
		return
	case bool:
		sb.WriteString(strconv.FormatBool(x))
		return
	case string:
		writePyString(sb, x)
		return
	}
	if m, ok := AsObj(v); ok {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		sb.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				sb.WriteString(", ")
			}
			writePyString(sb, k)
			sb.WriteString(": ")
			writePyJSON(sb, m[k])
		}
		sb.WriteByte('}')
		return
	}
	if list, ok := AsList(v); ok {
		sb.WriteByte('[')
		for i, e := range list {
			if i > 0 {
				sb.WriteString(", ")
			}
			writePyJSON(sb, e)
		}
		sb.WriteByte(']')
		return
	}
	if isNumber(v) {
		sb.WriteString(fmtNumber(v))
		return
	}
	writePyString(sb, fmt.Sprint(v))
}

func writePyString(sb *strings.Builder, s string) {
	sb.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			sb.WriteString(`\"`)
		case r == '\\':
			sb.WriteString(`\\`)
		case r == '\n':
			sb.WriteString(`\n`)
		case r == '\r':
			sb.WriteString(`\r`)
		case r == '\t':
			sb.WriteString(`\t`)
		case r == '\b':
			sb.WriteString(`\b`)
		case r == '\f':
			sb.WriteString(`\f`)
		case r < 0x20 || (r > 0x7f && r < 0x10000):
			fmt.Fprintf(sb, `\u%04x`, r)
		case r >= 0x10000:
			r -= 0x10000
			fmt.Fprintf(sb, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
}

// ActionLines renders one action: the head line, one line per change, remarks, and the guide hint
// for a blocked action.
func ActionLines(a *Action) []string {
	head := fmt.Sprintf("  %s %s %s", Symbol[a.Type], a.Kind, a.Key)
	if a.Type == Create && a.Summary != "" {
		head += "  " + a.Summary
	}
	if a.Type == Update && a.Label != "" {
		head += "   " + a.Label
	}
	if a.Type == Blocked && a.Reason != "" {
		head += "  " + a.Reason
	}
	if a.Type == Deferred {
		reason := a.Reason
		if reason == "" {
			reason = "deferred"
		}
		head += fmt.Sprintf("  %s — %s", a.Summary, reason)
	}
	if len(a.Notes) > 0 {
		head += "  " + strings.Join(a.Notes, " ")
	}
	lines := []string{head}
	for _, c := range a.Changes {
		isGroup := strings.HasPrefix(c.Path, "serviceGroups[")
		after, afterStr := c.After.(string)
		before, beforeStr := c.Before.(string)
		switch {
		case c.Before == nil && afterStr && isGroup:
			lines = append(lines, fmt.Sprintf("    + %s  %s", c.Path, after))
		case c.After == nil && beforeStr && isGroup:
			lines = append(lines, fmt.Sprintf("    - %s  %s", c.Path, before))
		default:
			lines = append(lines, fmt.Sprintf("      %s:  %s → %s", c.Path, FmtValue(c.Before), FmtValue(c.After)))
		}
	}
	for _, r := range a.Remarks {
		lines = append(lines, "      "+r)
	}
	if a.Type == Blocked && a.Topic != "" {
		lines = append(lines, "      See: cbctl capella guide "+a.Topic)
	}
	return lines
}

// RenderPlanText is the plan shown before a change. quiet prints only the summary line unless
// something is blocked.
func RenderPlanText(p *Plan, header string, quiet bool) string {
	if quiet && len(p.Blocked()) == 0 {
		return redact.String(p.SummaryLine())
	}
	var out []string
	if header != "" {
		out = append(out, header, "")
	}
	for _, a := range p.Visible() {
		out = append(out, ActionLines(a)...)
	}
	if len(p.Unmanaged) > 0 {
		out = append(out, "", "Unmanaged (not in capella.yaml; use --prune to delete):")
		for _, label := range p.Unmanaged {
			out = append(out, "    "+label)
		}
	}
	out = append(out, "", p.SummaryLine())
	return redact.String(strings.Join(out, "\n"))
}

// RenderPlanJSON is {"plan": {...}} with secrets masked.
func RenderPlanJSON(p *Plan) map[string]any {
	out, _ := redact.Object(map[string]any{"plan": p.ToJSON()}).(map[string]any)
	return out
}

// RenderApplyJSON is {"plan": {...}, "result": {...}} with secrets masked.
func RenderApplyJSON(p *Plan, r *ApplyResult) map[string]any {
	out, _ := redact.Object(map[string]any{"plan": p.ToJSON(), "result": r.ToJSON()}).(map[string]any)
	return out
}

// RenderSummaryTable is the KIND NAME ACTION STATUS DURATION table printed after apply. Hidden and
// noop actions are skipped; it returns "" when no rows remain.
func RenderSummaryTable(actions []*Action) string {
	rows := [][]string{{"KIND", "NAME", "ACTION", "STATUS", "DURATION"}}
	for _, a := range actions {
		if a.Hidden || a.Type == Noop {
			continue
		}
		dur := "-"
		if a.Duration != 0 {
			dur = FormatDuration(a.Duration)
		}
		rows = append(rows, []string{a.Kind, a.Key, string(a.Type), a.Status, dur})
	}
	if len(rows) == 1 {
		return ""
	}
	widths := make([]int, 5)
	for _, r := range rows {
		for i, c := range r {
			if n := utf8.RuneCountInString(c); n > widths[i] {
				widths[i] = n
			}
		}
	}
	lines := make([]string, len(rows))
	for i, r := range rows {
		cells := make([]string, len(r))
		for j, c := range r {
			cells[j] = c + strings.Repeat(" ", widths[j]-utf8.RuneCountInString(c))
		}
		lines[i] = strings.TrimRight(strings.Join(cells, "  "), " ")
	}
	return redact.String(strings.Join(lines, "\n"))
}
