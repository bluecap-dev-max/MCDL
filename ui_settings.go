package main

import (
	"strconv"
	"strings"
)

// 设置项类型。
const (
	setText = iota
	setInt
	setBool
)

// setRow 是一个可编辑的设置项。
type setRow struct {
	label string
	kind  int
	get   func() string
	set   func(string)
	step  func(int)
}

// settingRows 返回全部设置项。
func (a *App) settingRows() []setRow {
	boolRow := func(label string, ptr *bool) setRow {
		return setRow{
			label: label, kind: setBool,
			get: func() string {
				if *ptr {
					return "开"
				}
				return "关"
			},
			set: func(v string) {
				switch v {
				case "开", "on", "true", "1":
					*ptr = true
				case "关", "off", "false", "0":
					*ptr = false
				default:
					*ptr = !*ptr
				}
			},
		}
	}
	textRow := func(label string, ptr *string) setRow {
		return setRow{
			label: label, kind: setText,
			get: func() string { return *ptr },
			set: func(v string) { *ptr = v },
		}
	}
	return []setRow{
		{
			label: "目标启动器", kind: setText,
			get:  func() string { return a.launcherName() },
			set:  func(v string) { a.applyLauncher(v) },
			step: func(d int) { a.cycleLauncher(d) },
		},
		{
			label: "输出目录", kind: setText,
			get: func() string { return a.cfg.OutputDir },
			set: func(v string) { a.cfg.OutputDir = v; a.cfg.normalize() },
		},
		{
			label: "输出布局", kind: setText,
			get: func() string { return humanLayout(a.cfg.Layout) },
			set: func(v string) {
				if isLayoutName(v) {
					a.cfg.Layout = v
				}
			},
			step: func(d int) { a.cfg.Layout = nextLayout(a.cfg.Layout, d) },
		},
		{
			label: "下载源", kind: setText,
			get: func() string { return sourceLabel(a.cfg.Source) },
			set: func(v string) {
				if isSourceName(v) {
					a.cfg.Source = v
				}
			},
			step: func(d int) { a.cfg.Source = nextSource(a.cfg.Source, d) },
		},
		{
			label: "并发数", kind: setInt,
			get: func() string { return strconv.Itoa(a.cfg.Concurrency) },
			set: func(v string) {
				if n, err := strconv.Atoi(v); err == nil {
					a.cfg.Concurrency = n
					a.cfg.normalize()
				}
			},
			step: func(d int) { a.cfg.Concurrency += d; a.cfg.normalize() },
		},
		boolRow("下载客户端", &a.cfg.DownloadClient),
		boolRow("下载服务端", &a.cfg.DownloadServer),
		boolRow("官方映射", &a.cfg.DownloadMapping),
		boolRow("资源文件", &a.cfg.DownloadAssets),
		boolRow("依赖库", &a.cfg.DownloadLibs),
		boolRow("解包原生库", &a.cfg.ExtractNatives),
		boolRow("launcher_profiles", &a.cfg.LauncherProfile),
		boolRow("断点续传", &a.cfg.Resume),
		boolRow("SHA1 校验", &a.cfg.VerifySHA1),
		boolRow("跳过已存在", &a.cfg.SkipExisting),
		textRow("镜像·主文件", &a.cfg.MirrorData),
		textRow("镜像·资源", &a.cfg.MirrorAssets),
		textRow("镜像·依赖库", &a.cfg.MirrorLibraries),
		textRow("镜像·元数据", &a.cfg.MirrorMeta),
	}
}

// launcherName 返回当前目标启动器的名字。
func (a *App) launcherName() string {
	if a.cfg.Launcher == "" {
		return "自定义"
	}
	if p, ok := launcherByID(a.cfg.Launcher); ok {
		return p.Name
	}
	return "自定义"
}

// launcherOptions 返回可选的目标启动器（含自定义）。
func (a *App) launcherOptions() []launcherPreset {
	return knownLaunchers()
}

// cycleLauncher 在启动器之间循环切换（←→）。
func (a *App) cycleLauncher(delta int) {
	list := a.launcherOptions()
	idx := -1
	for i, p := range list {
		if p.ID == a.cfg.Launcher {
			idx = i
			break
		}
	}
	idx = (idx + delta + len(list) + 1) % (len(list) + 1)
	if idx >= len(list) {
		a.cfg.Launcher = ""
		a.notify("目标启动器：自定义")
	} else {
		a.applyLauncher(list[idx].ID)
	}
}

// applyLauncher 套用某个启动器的目录与布局。
func (a *App) applyLauncher(id string) {
	p, ok := launcherByID(id)
	if !ok {
		a.cfg.Launcher = ""
		a.notify("目标启动器：自定义")
		return
	}
	a.cfg.Launcher = p.ID
	a.cfg.OutputDir = p.Dir
	a.cfg.Layout = p.Layout
	a.cfg.normalize()
	a.notify("目标启动器：" + p.Name + " → " + p.Dir)
	a.pushLog("目标启动器：" + p.Name + "（" + p.Dir + "）")
}

// layoutHint 返回当前布局的说明。
func (a *App) layoutHint() string {
	switch a.cfg.Layout {
	case layoutPCL:
		return "PCL 友好：标准 .minecraft + launcher_profiles.json（PCL/官方直接指这个目录）"
	case layoutPrism:
		return "Prism/MultiMC：生成 instances/<版本>/minecraft/ 与 instance.cfg"
	default:
		return "标准 .minecraft：HMCL / PCL / ATLauncher / 官方启动器通用"
	}
}

// handleSettingsKey 处理设置界面的按键。
func (a *App) handleSettingsKey(k Key) {
	rows := a.settingRows()
	switch k.Kind {
	case KeyEsc:
		a.overlay = 0
		return
	case KeyUp:
		a.settingsFocus--
		if a.settingsFocus < 0 {
			a.settingsFocus = len(rows) - 1
		}
		return
	case KeyDown:
		a.settingsFocus++
		if a.settingsFocus >= len(rows) {
			a.settingsFocus = 0
		}
		return
	case KeyLeft:
		if a.settingsFocus < len(rows) && rows[a.settingsFocus].step != nil {
			rows[a.settingsFocus].step(-1)
			a.saveConfig()
		}
		return
	case KeyRight:
		if a.settingsFocus < len(rows) && rows[a.settingsFocus].step != nil {
			rows[a.settingsFocus].step(1)
			a.saveConfig()
		}
		return
	case KeyEnter:
		if a.settingsFocus < len(rows) {
			r := rows[a.settingsFocus]
			switch {
			case r.kind == setText && r.step != nil:
				// 可选项：回车即切换到下一个
				r.step(1)
				a.saveConfig()
			case r.kind == setText:
				a.editing = true
				a.editBuf = r.get()
			default:
				r.set("")
				a.saveConfig()
			}
		}
		return
	case KeyRune:
		switch k.R {
		case 'q':
			a.overlay = 0
		case 's':
			a.saveConfig()
			a.overlay = 0
		case ' ':
			if a.settingsFocus < len(rows) && rows[a.settingsFocus].kind == setBool {
				rows[a.settingsFocus].set("")
				a.saveConfig()
			}
		}
	}
}

// commitEdit 提交编辑中的设置项。
func (a *App) commitEdit() {
	rows := a.settingRows()
	if a.settingsFocus < len(rows) {
		rows[a.settingsFocus].set(a.editBuf)
		a.saveConfig()
	}
	a.editing = false
}

// saveConfig 保存配置到磁盘。
func (a *App) saveConfig() {
	if err := a.cfg.Save(); err != nil {
		a.notify("保存配置失败: " + err.Error())
	}
}

// settingsOverlay 渲染设置浮层。
func (a *App) settingsOverlay(w, h int) []string {
	rows := a.settingRows()
	boxW := 62
	if w < boxW+4 {
		boxW = w - 4
	}
	if boxW < 30 {
		boxW = 30
	}
	lines := []string{}
	lines = append(lines, "┌"+fillTo(" 设置 ", boxW-2, "─")+"┐")
	for i, r := range rows {
		mark := "  "
		if i == a.settingsFocus {
			mark = "> "
		}
		val := r.get()
		if r.kind == setBool {
			if val == "开" {
				val = "[ 开 ]"
			} else {
				val = "[ 关 ]"
			}
		}
		if a.editing && i == a.settingsFocus {
			val = a.editBuf + "▌"
		}
		lines = append(lines, "│"+padRight(mark+r.label, 18)+padRight(val, boxW-20)+"│")
	}
	lines = append(lines, "│"+padRight(" ↑↓选择 ←→调整 空格/回车切换或编辑 s保存 Esc关闭", boxW-2)+"│")
	if p, ok := launcherByID(a.cfg.Launcher); ok {
		lines = append(lines, "│"+padRight(" "+p.Name+"："+p.Hint, boxW-2)+"│")
	}
	lines = append(lines, "│"+padRight(" "+a.layoutHint(), boxW-2)+"│")
	lines = append(lines, "└"+strings.Repeat("─", boxW-2)+"┘")
	return lines
}

// helpOverlay 渲染帮助浮层。
func (a *App) helpOverlay(w, h int) []string {
	items := [][2]string{
		{"↑ ↓ / j k", "上下移动光标"},
		{"空格", "勾选 / 取消勾选当前版本"},
		{"a / A", "全选当前筛选结果 / 清空选择"},
		{"Enter", "开始下载选中的版本"},
		{"p", "暂停 / 继续下载"},
		{"c", "取消当前下载"},
		{"r", "重新检查并补齐缺失文件"},
		{"o", "打开输出目录"},
		{"x", "打包成整合包 zip（PCL 可直接导入）"},
		{"f / Tab", "切换版本类型筛选"},
		{"/", "搜索版本号"},
		{"v", "切换排序（最新 / 最旧）"},
		{"s", "设置（目录、布局、并发、内容、镜像）"},
		{"q", "退出"},
	}
	boxW := 56
	if w < boxW+4 {
		boxW = w - 4
	}
	lines := []string{"┌" + fillTo(" 帮助 ", boxW-2, "─") + "┐"}
	for _, it := range items {
		lines = append(lines, "│"+padRight(" "+it[0], 14)+padRight(it[1], boxW-16)+"│")
	}
	lines = append(lines, "│"+padRight(" 按任意键关闭 ", boxW-2)+"│")
	lines = append(lines, "└"+strings.Repeat("─", boxW-2)+"┘")
	return lines
}

// overlayLines 把浮层覆盖到主界面上。
func overlayLines(base []string, box []string, w, h int) []string {
	out := append([]string(nil), base...)
	top := (h - len(box)) / 2
	if top < 0 {
		top = 0
	}
	left := (w - boxWidth(box)) / 2
	if left < 0 {
		left = 0
	}
	for i, bl := range box {
		row := top + i
		if row < 0 || row >= len(out) {
			continue
		}
		out[row] = overlayLine(out[row], bl, left, w)
	}
	return out
}

func boxWidth(box []string) int {
	m := 0
	for _, l := range box {
		if stringWidth(l) > m {
			m = stringWidth(l)
		}
	}
	return m
}

// overlayLine 把浮层的一行覆盖到主界面的一行上。
func overlayLine(base, over string, left, w int) string {
	ow := stringWidth(over)
	if left+ow > w {
		over = truncateWidth(over, w-left)
		ow = stringWidth(over)
	}
	head := padRight(truncateWidth(base, left), left)
	tailStart := byteOffset(base, left+ow)
	tail := ""
	if tailStart < len(base) {
		tail = base[tailStart:]
	}
	tail = truncateWidth(tail, w-left-ow)
	return head + over + padRight(tail, w-left-ow)
}

// byteOffset 返回第 n 列对应的字节偏移。
func byteOffset(s string, col int) int {
	c := 0
	for i, r := range s {
		if c >= col {
			return i
		}
		c += runeWidth(r)
	}
	return len(s)
}
