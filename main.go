package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// Options 是命令行参数。
type Options struct {
	OutputDir     string
	All           bool
	Versions      []string
	Types         []string
	Client        bool
	Server        bool
	Mappings      bool
	Assets        bool
	Libraries     bool
	Natives       bool
	Concurrency   int
	CLI           bool
	ASCII         bool
	Help          bool
	Render        bool
	DryRun        bool
	ListLaunchers bool
	Layout        string
	Launcher      string
	Source        string
	ExportZip     string
	fs            *flag.FlagSet
}

// stringSlice 支持重复出现的命令行参数。
type stringSlice []string

func (s *stringSlice) String() string { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func parseFlags() *Options {
	o := &Options{}
	fs := flag.NewFlagSet("mcdl", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	var versions, types stringSlice
	fs.StringVar(&o.OutputDir, "out", "", "输出目录（默认 %USERPROFILE%\\.minecraft）")
	fs.StringVar(&o.OutputDir, "o", "", "输出目录（同 -out）")
	fs.BoolVar(&o.All, "all", false, "选择全部版本")
	fs.Var(&versions, "version", "指定版本号，可重复使用")
	fs.Var(&types, "type", "限定版本类型: release/snapshot/old_beta/old_alpha，可重复")
	fs.BoolVar(&o.Client, "client", false, "只下载客户端")
	fs.BoolVar(&o.Server, "server", false, "只下载服务端")
	fs.BoolVar(&o.Mappings, "mappings", false, "下载官方反混淆映射")
	fs.BoolVar(&o.Assets, "assets", false, "下载资源文件")
	fs.BoolVar(&o.Libraries, "libraries", false, "下载依赖库")
	fs.BoolVar(&o.Natives, "natives", false, "解包原生库到 natives 目录")
	fs.IntVar(&o.Concurrency, "concurrency", 0, "并发下载数")
	fs.StringVar(&o.Layout, "layout", "", "输出布局: standard / pcl / prism")
	fs.StringVar(&o.Launcher, "launcher", "", "目标启动器: pcl / hmcl / mojang / prism / multimc")
	fs.StringVar(&o.Source, "source", "", "下载源: auto / official / bmclapi / bmclapi-alt / custom")
	fs.StringVar(&o.ExportZip, "export-zip", "", "下载完成后打包成整合包 zip")
	fs.BoolVar(&o.CLI, "cli", false, "非交互命令行模式")
	fs.BoolVar(&o.DryRun, "dry-run", false, "只显示下载计划，不实际下载")
	fs.BoolVar(&o.ListLaunchers, "launchers", false, "列出检测到的启动器及其游戏目录")
	fs.BoolVar(&o.ASCII, "ascii", false, "使用纯 ASCII 界面")
	fs.BoolVar(&o.Render, "render", false, "调试用：只渲染一屏界面并退出")
	fs.BoolVar(&o.Help, "help", false, "显示帮助")
	fs.Usage = func() { printUsage(fs) }
	_ = fs.Parse(os.Args[1:])
	o.Versions = versions
	o.Types = types
	o.fs = fs
	return o
}

func printUsage(fs *flag.FlagSet) {
	fmt.Println(`mcdl - Minecraft 全版本下载器 (TUI)

用法:
  mcdl                          打开交互式界面
  mcdl --cli --all              命令行下载全部版本
  mcdl --cli --version 1.21.4   命令行下载指定版本

参数:`)
	fs.PrintDefaults()
	fmt.Println(`
示例:
  mcdl --cli --all --out D:\MC
  mcdl --cli --all --type release --type snapshot --assets --concurrency 12
  mcdl --cli --version 1.21.4 --version 1.20.1 --client --server`)
}

func applyOptions(cfg *Config, o *Options) {
	if o.OutputDir != "" {
		cfg.OutputDir = o.OutputDir
	}
	if o.Concurrency > 0 {
		cfg.Concurrency = o.Concurrency
	}
	if o.ASCII {
		cfg.ASCII = true
	}
	if o.Layout != "" {
		if isLayoutName(o.Layout) {
			cfg.Layout = strings.ToLower(strings.TrimSpace(o.Layout))
		} else {
			fmt.Println("未知布局：" + o.Layout + "（可选 standard / pcl / prism）")
		}
	}
	if o.Launcher != "" {
		if p, ok := launcherByID(strings.ToLower(strings.TrimSpace(o.Launcher))); ok {
			cfg.Launcher = p.ID
			if o.OutputDir == "" {
				cfg.OutputDir = p.Dir
			}
			if o.Layout == "" {
				cfg.Layout = p.Layout
			}
			fmt.Printf("目标启动器：%s\n  目录：%s\n  布局：%s\n", p.Name, cfg.OutputDir, humanLayout(cfg.Layout))
		} else {
			fmt.Println("未知启动器：" + o.Launcher + "（可选 pcl / hmcl / mojang / prism / multimc）")
		}
	}
	if o.ExportZip != "" {
		cfg.ExportZip = o.ExportZip
	}
	if o.Source != "" {
		if isSourceName(o.Source) {
			cfg.Source = strings.ToLower(strings.TrimSpace(o.Source))
			fmt.Println("下载源：" + sourceLabel(cfg.Source))
		} else {
			fmt.Println("未知下载源：" + o.Source + "（可选 auto / official / bmclapi）")
		}
	}
	if o.Natives {
		cfg.ExtractNatives = true
	}
	// 只有显式指定了内容开关才覆盖默认
	if o.Client || o.Server || o.Mappings || o.Assets || o.Libraries {
		cfg.DownloadClient = o.Client
		cfg.DownloadServer = o.Server
		cfg.DownloadMapping = o.Mappings
		cfg.DownloadAssets = o.Assets
		cfg.DownloadLibs = o.Libraries
	}
	cfg.normalize()
}

func main() {
	opts := parseFlags()
	if opts.Help {
		printUsage(opts.fs)
		return
	}
	cfg := LoadConfig()
	firstRun := !fileExists(configPath())
	applyOptions(cfg, opts)
	// 首次运行且用户没指定目录时，自动套用检测到的启动器
	if firstRun && cfg.Launcher == "" && opts.OutputDir == "" && opts.Launcher == "" {
		if best, ok := pickBestLauncher(); ok {
			cfg.Launcher = best.ID
			cfg.OutputDir = best.Dir
			cfg.Layout = best.Layout
			fmt.Printf("检测到 %s，游戏目录：%s\n", best.Name, best.Dir)
		}
	}
	_ = cfg.Save()

	if opts.Render {
		renderTest(cfg, opts)
		return
	}

	if opts.ListLaunchers {
		list := detectLaunchers()
		if len(list) == 0 {
			fmt.Println("没有检测到已安装的启动器。")
			return
		}
		fmt.Println("检测到的启动器：")
		for _, p := range list {
			fmt.Printf("  %-16s %s\n", p.Name, p.Dir)
			fmt.Printf("  %-16s 布局: %s\n", "", humanLayout(p.Layout))
		}
		return
	}

	if opts.CLI {
		os.Exit(runCLI(cfg, opts))
	}

	term, err := NewConsole()
	force := os.Getenv("MCDL_FORCE_TUI") != ""
	if err != nil || (!force && !term.IsTerminal()) {
		if err != nil {
			fmt.Println("无法打开控制台：", err)
		} else {
			fmt.Println("当前不是交互式终端，请使用 --cli 模式。")
		}
		opts.CLI = true
		os.Exit(runCLI(cfg, opts))
	}
	app := newApp(cfg, opts, term)
	app.run()
	term.Close()
}

// renderTest 只渲染界面（用于排版调试）。
func renderTest(cfg *Config, opts *Options) {
	term, _ := NewConsole()
	a := newApp(cfg, opts, term)
	m, err := fetchManifest(context.Background())
	if err != nil {
		fmt.Println("获取清单失败：", err)
		return
	}
	a.manifest = m
	a.loaded = true
	a.entries = m.Versions
	a.rebuildView()
	a.applyInitialSelection()
	a.pushLog("已获取 922 个版本（正式版 103 / 快照 758 / Beta 26 / Alpha 35）")
	for _, size := range [][2]int{{120, 40}, {100, 30}, {80, 24}} {
		fmt.Printf("=== %dx%d ===\n", size[0], size[1])
		for _, l := range a.layout(size[0], size[1]) {
			fmt.Println(l)
		}
		fmt.Println()
	}
	a.overlay = 2
	fmt.Println("=== 设置浮层 ===")
	for _, l := range a.layout(100, 30) {
		fmt.Println(l)
	}
	a.overlay = 1
	fmt.Println("=== 帮助浮层 ===")
	for _, l := range a.layout(100, 30) {
		fmt.Println(l)
	}
}

// printPlan 打印下载计划（--dry-run）。
func printPlan(eng *Engine) {
	kinds := map[string]int{}
	var libs []string
	for _, j := range eng.Jobs() {
		for _, t := range j.Tasks {
			kinds[t.Kind]++
			if t.Kind == "native" {
				libs = append(libs, t.Label)
			}
		}
	}
	fmt.Printf("下载源：%s\n", sourceLabel(eng.Source()))
	if p := eng.ProbeInfo(); p != "" {
		fmt.Printf("测速结果：%s\n", p)
	}
	fmt.Printf("版本数：%d   文件数：%d   总大小：%s\n",
		len(eng.Jobs()), len(eng.Tasks()), humanSize(eng.TotalSize()))
	order := []string{"json", "client", "server", "library", "native", "asset", "assetindex", "mapping"}
	for _, k := range order {
		if n := kinds[k]; n > 0 {
			fmt.Printf("  %-10s %d\n", k, n)
		}
	}
	if len(libs) > 0 {
		fmt.Printf("将解包的原生库：%d 个\n", len(libs))
		for _, l := range libs {
			fmt.Printf("  - %s\n", l)
		}
	}
	if os.Getenv("MCDL_PLAN_VERBOSE") != "" {
		fmt.Println("---- 全部任务 ----")
		for _, t := range eng.Tasks() {
			fmt.Printf("%-10s %s\n         -> %s\n", t.Kind, t.URL, t.Dest)
		}
	}
}

// runCLI 是非交互模式。
func runCLI(cfg *Config, opts *Options) int {
	fmt.Println("正在获取 Mojang 版本清单…")
	m, err := fetchManifest(context.Background())
	if err != nil {
		fmt.Println("获取版本清单失败：", err)
		return 2
	}
	selected := pickVersions(m, opts)
	if len(selected) == 0 {
		fmt.Println("没有匹配的版本。")
		return 1
	}
	fmt.Printf("共 %d 个版本待下载，输出目录：%s\n", len(selected), cfg.OutputDir)

	eng := NewEngine(cfg)
	eng.SetLogger(func(s string) { fmt.Println(s) })
	eng.SetProgress(func(done, total int, phase string) {
		fmt.Printf("\r%s %d/%d", phase, done, total)
	})
	if err := eng.Prepare(context.Background(), selected); err != nil {
		fmt.Println("准备失败：", err)
		return 3
	}
	if opts.DryRun {
		printPlan(eng)
		return 0
	}
	fmt.Printf("\n计划就绪：%d 个文件，总计 %s，开始下载…\n", len(eng.Jobs()), humanSize(eng.TotalSize()))

	done := make(chan struct{})
	go func() {
		eng.Run()
		eng.Wait()
		close(done)
	}()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			goto finished
		case <-ticker.C:
			total := eng.TotalSize()
			written := eng.Written()
			pct := 0.0
			if total > 0 {
				pct = float64(written) / float64(total) * 100
			}
			if pct > 100 {
				pct = 100
			}
			fmt.Printf("\r进度 %6.2f%%  %s / %s   %s/s     ", pct,
				humanSize(written), humanSize(total), humanSize(int64(eng.Speed())))
		}
	}
finished:
	if err := eng.Finalize(selectedIDs(selected)); err != nil {
		fmt.Println("收尾失败：", err)
	}
	failed := 0
	for _, j := range eng.Jobs() {
		failed += int(j.Fail())
	}
	if cfg.ExportZip != "" {
		zipPath := cfg.ExportZip
		fmt.Printf("正在打包整合包 %s …\n", zipPath)
		lastPct := -1
		prog := func(done, total int64) {
			if total <= 0 {
				return
			}
			pct := int(float64(done) / float64(total) * 100)
			if pct != lastPct && pct%5 == 0 {
				lastPct = pct
				fmt.Printf("\r打包 %3d%%  %s / %s   ", pct, humanSize(done), humanSize(total))
			}
		}
		if err := exportModpackZip(cfg.OutputDir, zipPath, prog, func(m string) {
			fmt.Println("\n提示：" + m)
		}); err != nil {
			fmt.Println("\n打包失败：", err)
		} else {
			fmt.Println("\n已打包：", zipPath)
		}
	}
	fmt.Printf("\n完成。失败文件 %d 个。目录：%s\n", failed, cfg.OutputDir)
	if failed > 0 {
		return 1
	}
	return 0
}

// selectedIDs 从版本列表里取出所有版本号。
func selectedIDs(entries []ManifestEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ID)
	}
	return out
}

// pickVersions 按命令行参数挑选版本。
func pickVersions(m *Manifest, opts *Options) []ManifestEntry {
	want := map[string]bool{}
	for _, v := range opts.Versions {
		want[v] = true
	}
	typeFilter := map[string]bool{}
	for _, t := range opts.Types {
		typeFilter[strings.ToLower(t)] = true
	}
	var out []ManifestEntry
	for _, e := range m.Versions {
		if len(want) > 0 {
			if want[e.ID] {
				out = append(out, e)
			}
			continue
		}
		if len(typeFilter) > 0 && !typeFilter[e.Type] {
			continue
		}
		out = append(out, e)
	}
	if len(want) == 0 && len(typeFilter) == 0 && !opts.All {
		// 未指定任何过滤条件时，默认只下载正式版
		var rel []ManifestEntry
		for _, e := range out {
			if e.Type == "release" {
				rel = append(rel, e)
			}
		}
		return rel
	}
	return out
}
