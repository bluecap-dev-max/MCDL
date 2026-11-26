package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// Key 是终端按键。
type Key struct {
	Kind  int
	R     rune
	Ctrl  bool
	Shift bool
	Alt   bool
}

// 按键类型。
const (
	KeyRune = iota
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyPgUp
	KeyPgDn
	KeyEnter
	KeyEsc
	KeyBackspace
	KeyTab
	KeyDelete
	KeyCtrlC
)

// Event 是界面事件。
type Event struct {
	Log       string
	Manifest  *Manifest
	ManifestE error
	PrepDone  int
	PrepAll   int
	Phase     string
	Notify    string
}

// 过滤器。
var filterNames = []string{"全部", "正式版", "快照", "Beta", "Alpha"}

// App 是 TUI 主程序。
type App struct {
	cfg  *Config
	opts *Options
	term *Console

	entries  []ManifestEntry
	view     []ManifestEntry
	manifest *Manifest
	loaded   bool
	loadErr  string

	filter    int
	search    string
	searching bool
	reversed  bool

	cursor   int
	offset   int
	pageRows int
	selected map[string]bool

	engine *Engine
	jobs   []*Job
	state  string

	logs    []string
	events  chan Event
	keys    chan Key
	quit    bool
	overlay int // 0 无 1 帮助 2 设置

	settingsFocus int
	editing       bool
	editBuf       string

	lastBytes   int64
	lastTime    time.Time
	speed       float64
	startTime   time.Time
	statusMsg   string
	statusUntil time.Time

	packing   int32
	packDone  int64
	packTotal int64
	packPath  string
}

// newApp 创建应用。
func newApp(cfg *Config, opts *Options, term *Console) *App {
	a := &App{
		cfg:      cfg,
		opts:     opts,
		term:     term,
		selected: map[string]bool{},
		events:   make(chan Event, 4096),
		keys:     make(chan Key, 64),
		state:    "就绪",
	}
	a.lastTime = time.Now()
	return a
}

// run 启动主循环。
func (a *App) run() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go a.readKeys(ctx)
	go a.loadManifest(ctx)

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	a.term.Write("\x1b[?25l")
	for !a.quit {
		a.render()
		select {
		case k := <-a.keys:
			a.handleKey(k)
		case ev := <-a.events:
			a.handleEvent(ev)
		case <-ticker.C:
			a.tick()
		}
	}
	a.term.Write("\x1b[?25h\x1b[0m\r\n")
}

// readKeys 持续读取按键。
func (a *App) readKeys(ctx context.Context) {
	var logFile *os.File
	if os.Getenv("MCDL_KEYLOG") != "" {
		logFile, _ = os.OpenFile(filepath.Join(os.TempDir(), "mcdl-keys.log"),
			os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if logFile != nil {
			fmt.Fprintf(logFile, "=== 启动 %s ===\n", time.Now().Format("15:04:05"))
		}
	}
	for {
		k, err := a.term.ReadKey()
		if err != nil {
			if logFile != nil {
				fmt.Fprintf(logFile, "读取结束: %v\n", err)
			}
			return
		}
		if logFile != nil {
			fmt.Fprintf(logFile, "kind=%d rune=%q ctrl=%v shift=%v alt=%v\n",
				k.Kind, string(k.R), k.Ctrl, k.Shift, k.Alt)
			logFile.Sync()
		}
		select {
		case a.keys <- k:
		case <-ctx.Done():
			return
		}
	}
}

// loadManifest 后台拉取版本清单。
func (a *App) loadManifest(ctx context.Context) {
	m, err := fetchManifest(ctx)
	if err != nil {
		a.events <- Event{ManifestE: err}
		return
	}
	a.events <- Event{Manifest: m}
}

// handleEvent 处理界面事件。
func (a *App) handleEvent(ev Event) {
	if ev.Notify != "" {
		a.notify(ev.Notify)
	}
	switch ev.Phase {
	case "run":
		a.state = "下载中"
	case "finish":
		a.state = "已完成"
	}
	if ev.Log != "" {
		a.pushLog(ev.Log)
		return
	}
	switch {
	case ev.ManifestE != nil:
		a.loadErr = ev.ManifestE.Error()
		a.state = "清单获取失败"
	case ev.Manifest != nil:
		a.manifest = ev.Manifest
		a.loaded = true
		a.entries = append([]ManifestEntry(nil), ev.Manifest.Versions...)
		a.rebuildView()
		a.applyInitialSelection()
		a.pushLog(fmt.Sprintf("已获取 %d 个版本（正式版 %d / 快照 %d / Beta %d / Alpha %d）",
			len(a.entries), a.countType("release"), a.countType("snapshot"),
			a.countType("old_beta"), a.countType("old_alpha")))
		if d := describeDetected(); d != "" {
			a.pushLog("检测到的启动器：" + d)
			a.notify("已检测到启动器，按 s 可切换目标")
		}
	}
}

// tick 周期性刷新速度等指标。
func (a *App) tick() {
	if a.engine != nil {
		a.speed = a.engine.Speed()
	}
}

// pushLog 追加一条日志。
func (a *App) pushLog(s string) {
	a.logs = append(a.logs, s)
	if len(a.logs) > 200 {
		a.logs = a.logs[len(a.logs)-200:]
	}
}

// notify 临时提示。
func (a *App) notify(s string) {
	a.statusMsg = s
	a.statusUntil = time.Now().Add(3 * time.Second)
}

// countType 统计某种类型的版本数量。
func (a *App) countType(t string) int {
	n := 0
	for _, e := range a.entries {
		if e.Type == t {
			n++
		}
	}
	return n
}

// rebuildView 依据过滤器与搜索重建可见列表。
func (a *App) rebuildView() {
	a.view = a.view[:0]
	low := strings.ToLower(a.search)
	for _, e := range a.entries {
		if !a.matchFilter(e.Type) {
			continue
		}
		if low != "" && !strings.Contains(strings.ToLower(e.ID), low) {
			continue
		}
		a.view = append(a.view, e)
	}
	if a.reversed {
		for i, j := 0, len(a.view)-1; i < j; i, j = i+1, j-1 {
			a.view[i], a.view[j] = a.view[j], a.view[i]
		}
	}
	if a.cursor >= len(a.view) {
		a.cursor = len(a.view) - 1
	}
	if a.cursor < 0 {
		a.cursor = 0
	}
}

// matchFilter 判断版本类型是否符合当前过滤器。
func (a *App) matchFilter(t string) bool {
	switch a.filter {
	case 1:
		return t == "release"
	case 2:
		return t == "snapshot"
	case 3:
		return t == "old_beta"
	case 4:
		return t == "old_alpha"
	}
	return true
}

// applyInitialSelection 处理命令行参数指定的初始选择。
func (a *App) applyInitialSelection() {
	if a.opts == nil {
		return
	}
	for _, v := range a.opts.Versions {
		for _, e := range a.entries {
			if e.ID == v {
				a.selected[v] = true
			}
		}
	}
	if a.opts.All {
		for _, e := range a.view {
			a.selected[e.ID] = true
		}
	}
}

// selectedIDs 返回当前选中的版本号。
func (a *App) selectedIDs() []string {
	var out []string
	for _, e := range a.entries {
		if a.selected[e.ID] {
			out = append(out, e.ID)
		}
	}
	return out
}

// handleKey 处理按键。
func (a *App) handleKey(k Key) {
	if a.editing {
		a.handleEditKey(k)
		return
	}
	if a.searching {
		a.handleSearchKey(k)
		return
	}
	if a.overlay == 2 {
		a.handleSettingsKey(k)
		return
	}
	if a.overlay == 1 {
		a.overlay = 0
		return
	}
	switch k.Kind {
	case KeyCtrlC:
		a.quit = true
		return
	case KeyUp:
		a.moveCursor(-1)
		return
	case KeyDown:
		a.moveCursor(1)
		return
	case KeyPgUp:
		a.moveCursor(-a.pageSize())
		return
	case KeyPgDn:
		a.moveCursor(a.pageSize())
		return
	case KeyHome:
		a.cursor = 0
		return
	case KeyEnd:
		a.cursor = len(a.view) - 1
		return
	case KeyEnter:
		a.startDownload()
		return
	case KeyEsc:
		if a.overlay != 0 {
			a.overlay = 0
		}
		return
	case KeyTab:
		a.filter = (a.filter + 1) % len(filterNames)
		a.rebuildView()
		return
	case KeyBackspace:
		if a.search != "" {
			r := []rune(a.search)
			a.search = string(r[:len(r)-1])
			a.rebuildView()
		}
		return
	case KeyRune:
		a.handleRune(k)
		return
	}
}

// handleRune 处理字符按键。
func (a *App) handleRune(k Key) {
	// 兜底：把控制字符当对应按键处理
	switch k.R {
	case '\r', '\n':
		a.startDownload()
		return
	}
	if k.Ctrl {
		switch k.R {
		case 'a':
			a.selectAll()
			return
		case 'c':
			a.quit = true
			return
		case 'f':
			a.openFolder()
			return
		}
		return
	}
	switch k.R {
	case 'q', 'Q':
		a.quit = true
	case 'j':
		a.moveCursor(1)
	case 'k':
		a.moveCursor(-1)
	case 'u':
		a.moveCursor(-a.pageSize())
	case 'd':
		a.moveCursor(a.pageSize())
	case 'g':
		a.cursor = 0
	case 'G':
		a.cursor = len(a.view) - 1
	case ' ':
		a.toggleCurrent()
	case 'a':
		a.selectAll()
	case 'A':
		a.clearSelection()
	case 'f':
		a.filter = (a.filter + 1) % len(filterNames)
		a.rebuildView()
	case '/':
		a.searching = true
		a.editBuf = a.search
	case 'v':
		a.reversed = !a.reversed
		a.rebuildView()
	case 'p':
		a.togglePause()
	case 'c':
		a.cancelDownload()
	case 'r':
		a.retryFailed()
	case 'o':
		a.openFolder()
	case 'x':
		a.exportPack()
	case 'l':
		a.exportManifest()
	case 's':
		a.overlay = 2
		a.settingsFocus = 0
	case '?':
		a.overlay = 1
	}
}

// handleSearchKey 处理搜索模式下的按键。
func (a *App) handleSearchKey(k Key) {
	switch k.Kind {
	case KeyUp:
		a.moveCursor(-1)
	case KeyDown:
		a.moveCursor(1)
	case KeyPgUp:
		a.moveCursor(-a.pageSize())
	case KeyPgDn:
		a.moveCursor(a.pageSize())
	case KeyEnter:
		a.search = a.editBuf
		a.searching = false
		a.rebuildView()
	case KeyEsc:
		a.searching = false
		a.editBuf = a.search
	case KeyBackspace:
		r := []rune(a.editBuf)
		if len(r) > 0 {
			a.editBuf = string(r[:len(r)-1])
			a.search = a.editBuf
			a.rebuildView()
		}
	case KeyRune:
		a.editBuf += string(k.R)
		a.search = a.editBuf
		a.rebuildView()
	}
}

// handleEditKey 处理设置项编辑模式下的按键。
func (a *App) handleEditKey(k Key) {
	switch k.Kind {
	case KeyEnter:
		a.commitEdit()
	case KeyEsc:
		a.editing = false
	case KeyBackspace:
		r := []rune(a.editBuf)
		if len(r) > 0 {
			a.editBuf = string(r[:len(r)-1])
		}
	case KeyRune:
		a.editBuf += string(k.R)
	}
}

// moveCursor 移动光标。
func (a *App) moveCursor(d int) {
	a.cursor += d
	if a.cursor < 0 {
		a.cursor = 0
	}
	if a.cursor >= len(a.view) {
		a.cursor = len(a.view) - 1
	}
}

// pageSize 返回翻页步长（当前列表可见行数）。
func (a *App) pageSize() int {
	if a.pageRows > 0 {
		return a.pageRows
	}
	return 10
}

// toggleCurrent 勾选/取消当前版本。
func (a *App) toggleCurrent() {
	if a.cursor < 0 || a.cursor >= len(a.view) {
		return
	}
	id := a.view[a.cursor].ID
	if a.selected[id] {
		delete(a.selected, id)
	} else {
		a.selected[id] = true
	}
}

// selectAll 勾选当前视图内的全部版本。
func (a *App) selectAll() {
	for _, e := range a.view {
		a.selected[e.ID] = true
	}
	a.notify(fmt.Sprintf("已选中 %d 个版本", len(a.selected)))
}

// clearSelection 清空选择。
func (a *App) clearSelection() {
	a.selected = map[string]bool{}
	a.notify("已清空选择")
}

// openFolder 打开输出目录。
func (a *App) openFolder() {
	dir := a.cfg.OutputDir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.notify("创建目录失败: " + err.Error())
		return
	}
	if err := OpenFolder(dir); err != nil {
		a.notify("打开目录失败: " + err.Error())
		return
	}
	a.notify("已打开 " + dir)
}

// togglePause 暂停/继续。
func (a *App) togglePause() {
	if a.engine == nil {
		a.notify("当前没有下载任务")
		return
	}
	if a.engine.Paused() {
		a.engine.Resume()
		a.state = "下载中"
		a.notify("已继续")
	} else {
		a.engine.Pause()
		a.state = "已暂停"
		a.notify("已暂停")
	}
}

// cancelDownload 取消下载。
func (a *App) cancelDownload() {
	if a.engine == nil {
		a.notify("当前没有下载任务")
		return
	}
	a.engine.Cancel()
	a.state = "已取消"
	a.notify("已取消下载")
}

// retryFailed 重新执行（已存在的文件会自动跳过）。
func (a *App) retryFailed() {
	if a.engine == nil {
		a.notify("当前没有下载任务")
		return
	}
	if a.state == "下载中" || a.state == "准备中" {
		a.notify("任务仍在进行中")
		return
	}
	a.notify("重新检查并补齐缺失文件…")
	a.startDownload()
}

// exportManifest 导出「已下载版本清单」。
func (a *App) exportManifest() {
	if a.engine == nil {
		a.notify("还没有下载记录，先下载一些版本")
		return
	}
	eng := a.engine
	go func() {
		if err := eng.writeLocalManifest(); err != nil {
			a.events <- Event{Log: "[x] 导出清单失败: " + err.Error()}
			return
		}
		a.events <- Event{Log: "[*] 已导出清单: " + filepath.Join(a.cfg.OutputDir, "version_manifest.local.json")}
	}()
}

// exportPack 把输出目录打包成整合包 zip（PCL / Prism 都能直接导入）。
func (a *App) exportPack() {
	if atomic.LoadInt32(&a.packing) == 1 {
		a.notify("正在打包，请稍候…")
		return
	}
	dir := a.cfg.OutputDir
	zipPath := a.cfg.ExportZip
	if zipPath == "" {
		// 放在游戏目录旁边，避免把自己打进去
		zipPath = strings.TrimRight(dir, `\/`) + "-整合包.zip"
	}
	atomic.StoreInt64(&a.packDone, 0)
	atomic.StoreInt64(&a.packTotal, dirSize(dir))
	atomic.StoreInt32(&a.packing, 1)
	a.packPath = zipPath
	a.pushLog("开始打包 → " + zipPath)
	a.notify("正在打包…")

	progress := func(done, total int64) {
		atomic.StoreInt64(&a.packDone, done)
		atomic.StoreInt64(&a.packTotal, total)
	}
	warn := func(msg string) {
		a.events <- Event{Log: "[!] " + msg}
	}
	go func() {
		var err error
		if a.cfg.Layout == layoutPrism {
			err = exportZip(dir, zipPath, progress, warn)
		} else {
			err = exportModpackZip(dir, zipPath, progress, warn)
		}
		atomic.StoreInt32(&a.packing, 0)
		if err != nil {
			a.events <- Event{Log: "[x] 打包失败: " + err.Error()}
			a.events <- Event{Notify: "打包失败：" + err.Error()}
			return
		}
		a.events <- Event{
			Log:    "[*] 已打包整合包: " + zipPath,
			Notify: "打包完成: " + zipPath,
		}
	}()
}

// startDownload 开始下载。
func (a *App) startDownload() {
	if a.state == "准备中" || a.state == "下载中" {
		a.notify("已有任务在运行，请先取消")
		return
	}
	ids := a.selectedIDs()
	if len(ids) == 0 {
		a.notify("请先用空格勾选要下载的版本")
		return
	}
	entries := make([]ManifestEntry, 0, len(ids))
	for _, e := range a.entries {
		if a.selected[e.ID] {
			entries = append(entries, e)
		}
	}
	a.cfg.normalize()
	eng := NewEngine(a.cfg)
	eng.SetLogger(func(s string) {
		select {
		case a.events <- Event{Log: s}:
		default:
		}
	})
	eng.SetProgress(func(done, total int, phase string) {
		select {
		case a.events <- Event{PrepDone: done, PrepAll: total, Phase: phase}:
		default:
		}
	})
	a.engine = eng
	a.jobs = nil
	a.state = "准备中"
	a.startTime = time.Now()
	a.lastBytes = 0
	a.lastTime = time.Now()
	a.speed = 0
	a.pushLog(fmt.Sprintf("开始处理 %d 个版本，输出到 %s", len(entries), a.cfg.OutputDir))

	go func() {
		if err := eng.Prepare(context.Background(), entries); err != nil {
			a.events <- Event{Log: "[x] 准备失败: " + err.Error()}
			return
		}
		a.events <- Event{Log: fmt.Sprintf("计划就绪：%d 个版本，共 %d 个文件，%s",
			len(entries), len(eng.Jobs()), humanSize(eng.TotalSize())), Phase: "run"}
		eng.Run()
		eng.Wait()
		if ferr := eng.Finalize(ids); ferr != nil {
			a.events <- Event{Log: "[!] 收尾失败: " + ferr.Error()}
		}
		// 全部结束后统计
		fail := 0
		done := 0
		for _, j := range eng.Jobs() {
			fail += int(j.Fail())
			done++
		}
		a.events <- Event{Log: fmt.Sprintf("下载结束：失败 %d 个文件", fail), Phase: "finish"}
		if fail > 0 {
			a.events <- Event{Log: "按 r 可以重新检查并补齐缺失文件"}
		} else if p, ok := launcherByID(a.cfg.Launcher); ok {
			a.events <- Event{Log: fmt.Sprintf("现在打开 %s，%d 个版本已经出现在启动列表里，可以直接启动", p.Name, done)}
		} else {
			a.events <- Event{Log: fmt.Sprintf("下载完成：%d 个版本已就绪，%s", done, a.cfg.OutputDir)}
		}
	}()
}
