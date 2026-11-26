package main

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// render 重绘整屏。
func (a *App) render() {
	w, h := a.term.Size()
	if w < 60 {
		w = 60
	}
	if h < 16 {
		h = 16
	}
	lines := a.layout(w, h)
	var b strings.Builder
	b.WriteString("\x1b[H")
	for i, l := range lines {
		b.WriteString("\x1b[2K")
		b.WriteString(l)
		if i < len(lines)-1 {
			b.WriteString("\r\n")
		}
	}
	b.WriteString("\x1b[J")
	a.term.Write(b.String())
}

// layout 组装整屏内容。
func (a *App) layout(w, h int) []string {
	if !a.loaded {
		msg := "正在获取 Mojang 版本清单…"
		if a.loadErr != "" {
			msg = "获取失败：" + a.loadErr + "    (按 q 退出)"
		}
		lines := []string{"", "  " + msg, ""}
		return lines
	}

	header := []string{
		a.headerLine(w),
		a.filterLine(w),
		"",
	}
	footer := a.footerLines(w)
	bodyH := h - len(header) - len(footer)
	if bodyH < 6 {
		bodyH = 6
	}
	leftW := w * 45 / 100
	if leftW < 34 {
		leftW = 34
	}
	rightW := w - leftW - 1
	if rightW < 30 {
		rightW = 30
		leftW = w - rightW - 1
	}

	inner := bodyH - 2
	a.pageRows = inner
	left := boxLines("版本列表", leftW, a.versionRows(inner, leftW-2), bodyH)
	right := boxLines("下载任务", rightW, a.jobRows(inner, rightW-2), bodyH)

	// 左右并排
	merged := make([]string, 0, bodyH)
	for i := 0; i < bodyH; i++ {
		l := ""
		r := ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		merged = append(merged, l+" "+r)
	}

	lines := append([]string{}, header...)
	lines = append(lines, merged...)
	lines = append(lines, footer...)
	switch a.overlay {
	case 1:
		lines = overlayLines(lines, a.helpOverlay(w, h), w, h)
	case 2:
		lines = overlayLines(lines, a.settingsOverlay(w, h), w, h)
	}
	return lines
}

// headerLine 顶部标题行。
func (a *App) headerLine(w int) string {
	kind := sourceLabel(a.cfg.Source)
	if a.engine != nil {
		kind = sourceLabel(a.engine.Source())
	}
	left := " Minecraft 全版本下载器  mcdl"
	right := fmt.Sprintf("输出: %s  并发: %d  %s ", a.cfg.OutputDir, a.cfg.Concurrency, kind)
	if a.cfg.Launcher != "" {
		if p, ok := launcherByID(a.cfg.Launcher); ok {
			right = fmt.Sprintf("启动器: %s  输出: %s  并发: %d ", p.Name, a.cfg.OutputDir, a.cfg.Concurrency)
		}
	}
	avail := w - stringWidth(left) - 1
	if avail > 12 && stringWidth(right) > avail {
		prefix := "输出: "
		suffix := fmt.Sprintf("  并发: %d  %s ", a.cfg.Concurrency, kind)
		pathMax := avail - stringWidth(prefix) - stringWidth(suffix) - 2
		if pathMax > 8 {
			right = prefix + shortenPath(a.cfg.OutputDir, pathMax) + suffix
		} else {
			right = suffix
		}
	}
	return left + padLeft(right, w-stringWidth(left))
}

// shortenPath 路径过长时保留尾部并加省略号。
func shortenPath(p string, max int) string {
	if stringWidth(p) <= max {
		return p
	}
	if max <= 1 {
		return "…"
	}
	r := []rune(p)
	for len(r) > 0 {
		cand := "…" + string(r)
		if stringWidth(cand) <= max {
			return cand
		}
		r = r[1:]
	}
	return "…"
}

// filterLine 过滤/搜索/选择统计行。
func (a *App) filterLine(w int) string {
	parts := []string{}
	for i, n := range filterNames {
		if i == a.filter {
			parts = append(parts, "["+n+"]")
		} else {
			parts = append(parts, " "+n+" ")
		}
	}
	search := a.search
	if a.searching {
		search = a.editBuf + "▌"
	}
	s := fmt.Sprintf(" 筛选: %s   搜索: %s   已选 %d / %d",
		strings.Join(parts, " "), search, len(a.selected), len(a.entries))
	return truncateWidth(s, w)
}

// versionRows 渲染版本列表内容。
func (a *App) versionRows(height, width int) []string {
	if len(a.view) == 0 {
		return []string{"  （没有匹配的版本）"}
	}
	if a.cursor < a.offset {
		a.offset = a.cursor
	}
	if a.cursor >= a.offset+height {
		a.offset = a.cursor - height + 1
	}
	if a.offset < 0 {
		a.offset = 0
	}
	end := a.offset + height
	if end > len(a.view) {
		end = len(a.view)
	}

	// 自适应列宽：标记(1) 勾选(3) 空格(1) 版本号(可变) 空格(1) 类型(5) 空格(1) 日期(10)
	idw := 22
	datew := 10
	avail := width - 13
	if avail < idw+datew {
		datew = 0
	}
	if avail < idw {
		idw = avail
	}
	if idw < 8 {
		idw = 8
	}

	rows := make([]string, 0, height)
	for i := a.offset; i < end; i++ {
		e := a.view[i]
		mark := " "
		if i == a.cursor {
			mark = ">"
		}
		chk := "[ ]"
		if a.selected[e.ID] {
			chk = "[*]"
		}
		row := mark + chk + " " + padRight(e.ID, idw) + " " + padLeft(typeShort(e.Type), 5)
		if datew > 0 {
			row += " " + padRight(shortDate(e.ReleaseTime), datew)
		}
		rows = append(rows, row)
	}
	for len(rows) < height {
		rows = append(rows, "")
	}
	return rows
}

// jobRows 渲染任务面板。
func (a *App) jobRows(height, width int) []string {
	var jobs []*Job
	if a.engine != nil {
		jobs = a.engine.Jobs()
	}
	idw := 20
	if width-22 < idw {
		idw = width - 22
	}
	if idw < 10 {
		idw = 10
	}
	if len(jobs) == 0 {
		msg := "  （未开始下载）"
		if a.engine != nil && a.state == "准备中" {
			done, all := a.engine.Preparing()
			msg = fmt.Sprintf("  正在读取版本信息 %d/%d …", done, all)
		}
		rows := []string{msg}
		for len(rows) < height {
			rows = append(rows, "")
		}
		return rows
	}
	sort.SliceStable(jobs, func(i, j int) bool {
		return jobPriority(jobs[i]) < jobPriority(jobs[j])
	})
	rows := make([]string, 0, height)
	for _, j := range jobs {
		if len(rows) >= height {
			break
		}
		rows = append(rows, a.jobLine(j, idw))
	}
	for len(rows) < height {
		rows = append(rows, "")
	}
	return rows
}

// jobPriority 任务排序优先级：进行中 > 等待 > 失败 > 完成。
func jobPriority(j *Job) int {
	switch {
	case j.State() == stateRunning:
		return 0
	case j.State() == statePending:
		return 1
	case j.Fail() > 0:
		return 2
	default:
		return 3
	}
}

// jobLine 渲染单个版本的任务状态。
func (a *App) jobLine(j *Job, idw int) string {
	icon := " "
	switch {
	case j.Fail() > 0:
		icon = "!"
	case j.State() == stateDone:
		icon = "*"
	case j.State() == stateRunning:
		icon = ">"
	}
	total := j.Total()
	done := j.Done()
	pct := 0.0
	if total > 0 {
		pct = float64(done) / float64(total) * 100
	}
	extra := ""
	if j.Fail() > 0 {
		extra = fmt.Sprintf(" 失败%d", j.Fail())
	}
	return fmt.Sprintf("%s %s %s %s%s",
		icon, padRight(j.ID, idw), padLeft(fmt.Sprintf("%.0f%%", pct), 5),
		padLeft(humanSize(total), 9), extra)
}

// footerLines 渲染底部进度、状态、快捷键与日志。
func (a *App) footerLines(w int) []string {
	lines := []string{}

	// 打包中优先显示打包进度
	if atomic.LoadInt32(&a.packing) == 1 {
		done := atomic.LoadInt64(&a.packDone)
		total := atomic.LoadInt64(&a.packTotal)
		pct := 0.0
		if total > 0 {
			pct = float64(done) / float64(total) * 100
		}
		barW := 24
		if w < 70 {
			barW = 14
		}
		lines = append(lines, fmt.Sprintf(" 打包 [%s] %6.2f%%  %s / %s",
			progressBar(pct/100, barW), pct, humanSize(done), humanSize(total)))
		lines = append(lines, truncateWidth(" 正在打包整合包 → "+a.packPath+"（请勿关闭窗口）", w))
		lines = append(lines, truncateWidth(" 快捷键: 打包完成后会提示；期间可继续浏览列表，q 退出", w))
		logStart := len(a.logs) - 2
		if logStart < 0 {
			logStart = 0
		}
		for i := logStart; i < len(a.logs); i++ {
			lines = append(lines, truncateWidth(" · "+a.logs[i], w))
		}
		return lines
	}

	total := int64(0)
	written := int64(0)
	if a.engine != nil {
		total = a.engine.TotalSize()
		written = a.engine.Written()
	}
	pct := 0.0
	if total > 0 {
		pct = float64(written) / float64(total) * 100
	}
	if pct > 100 {
		pct = 100
	}
	barW := 24
	if w < 70 {
		barW = 14
	}
	bar := progressBar(pct/100, barW)
	eta := "--:--"
	if a.speed > 1024 && total > written {
		eta = humanDuration(float64(total-written) / a.speed)
	}
	lines = append(lines, fmt.Sprintf(" 进度 [%s] %6.2f%%  %s / %s   %s/s   剩余 %s",
		bar, pct, humanSize(written), humanSize(total), humanSize(int64(a.speed)), eta))

	status := a.state
	if a.statusMsg != "" && time.Now().Before(a.statusUntil) {
		status = a.statusMsg
	}
	tasks := 0
	failed := 0
	if a.engine != nil {
		for _, j := range a.engine.Jobs() {
			tasks += len(j.Tasks)
			failed += int(j.Fail())
		}
	}
	lines = append(lines, fmt.Sprintf(" 状态: %s   已选 %d 个版本   文件 %d   失败 %d",
		status, len(a.selected), tasks, failed))
	lines = append(lines, truncateWidth(" 移动: ↑↓/jk  PgUp/PgDn 翻页  Home/End 首尾  空格 勾选  a 全选  A 清空", w))
	lines = append(lines, truncateWidth(" 操作: Enter 开始  p 暂停  c 取消  r 补齐  o 打开  x 打包  s 设置  / 搜索  ? 帮助  q 退出", w))

	logStart := len(a.logs) - 2
	if logStart < 0 {
		logStart = 0
	}
	for i := logStart; i < len(a.logs); i++ {
		lines = append(lines, truncateWidth(" · "+a.logs[i], w))
	}
	return lines
}

// progressBar 渲染进度条。
func progressBar(p float64, width int) string {
	if width < 4 {
		return ""
	}
	if p < 0 {
		p = 0
	}
	if p > 1 {
		p = 1
	}
	filled := int(p * float64(width))
	if filled > width {
		filled = width
	}
	return strings.Repeat("#", filled) + strings.Repeat("-", width-filled)
}

// boxLines 渲染一个带边框的面板。
func boxLines(title string, w int, rows []string, total int) []string {
	if w < 6 {
		w = 6
	}
	inner := w - 2
	out := make([]string, 0, total)
	out = append(out, "┌"+fillTo(" "+title+" ", inner-1, "─")+"┐")
	for _, r := range rows {
		out = append(out, "│"+padRight(" "+r, inner)+"│")
	}
	out = append(out, "└"+strings.Repeat("─", inner)+"┘")
	for len(out) < total {
		out = append(out, "│"+strings.Repeat(" ", inner)+"│")
	}
	return out
}

// typeShort 版本类型的短名。
func typeShort(t string) string {
	switch t {
	case "release":
		return "正式"
	case "snapshot":
		return "快照"
	case "old_beta":
		return "Beta"
	case "old_alpha":
		return "Alpha"
	}
	return t
}

// shortDate 格式化发布日期。
func shortDate(s string) string {
	if s == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		if len(s) >= 10 {
			return s[:10]
		}
		return s
	}
	return t.Format("2006-01-02")
}
