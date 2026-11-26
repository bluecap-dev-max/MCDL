package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 布局：标准 .minecraft / PCL 友好 / MultiMC·Prism 实例
const (
	layoutStandard = "standard"
	layoutPCL      = "pcl"
	layoutPrism    = "prism"
)

// sharedDirName 是 prism 布局下共享资源目录的名字。
const sharedDirName = "_shared"

// layoutNames 是全部可选布局（供界面展示）。
var layoutNames = []string{layoutStandard, layoutPCL, layoutPrism}

// gameDir 返回某个版本的游戏目录（versions/assets/libraries 的根）。
func (e *Engine) gameDir(versionID string) string {
	if e.layout == layoutPrism {
		return filepath.Join(e.out, versionID, "minecraft")
	}
	return e.out
}

// sharedAssetsDir 返回资源目录（prism 布局下所有实例共用一份）。
func (e *Engine) sharedAssetsDir() string {
	if e.layout == layoutPrism {
		return filepath.Join(e.out, sharedDirName, "assets")
	}
	return filepath.Join(e.out, "assets")
}

// sharedLibrariesDir 返回依赖库目录（prism 布局下所有实例共用一份）。
func (e *Engine) sharedLibrariesDir() string {
	if e.layout == layoutPrism {
		return filepath.Join(e.out, sharedDirName, "libraries")
	}
	return filepath.Join(e.out, "libraries")
}

// nativesDir 返回原生库解包目录。
func (e *Engine) nativesDir(versionID string) string {
	if e.layout == layoutPrism {
		return filepath.Join(e.out, versionID, "minecraft", "natives")
	}
	return filepath.Join(e.out, "natives")
}

// writeInstanceCfg 生成 MultiMC / Prism 的实例描述文件。
func (e *Engine) writeInstanceCfg(versionID, name string) error {
	dir := filepath.Join(e.out, versionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("name=%s\nInstanceType=OneSix\nIntendedVersion=%s\n", name, versionID)
	return os.WriteFile(filepath.Join(dir, "instance.cfg"), []byte(body), 0o644)
}

// distributeShared 把共享的资源/依赖库复制到各个实例目录（prism 布局）。
func (e *Engine) distributeShared(versionIDs []string) error {
	if e.layout != layoutPrism {
		return nil
	}
	srcAssets := e.sharedAssetsDir()
	srcLibs := e.sharedLibrariesDir()
	for _, vid := range versionIDs {
		game := e.gameDir(vid)
		if err := copyTree(srcAssets, filepath.Join(game, "assets")); err != nil {
			return fmt.Errorf("复制资源到 %s 失败: %w", vid, err)
		}
		if err := copyTree(srcLibs, filepath.Join(game, "libraries")); err != nil {
			return fmt.Errorf("复制依赖库到 %s 失败: %w", vid, err)
		}
	}
	return nil
}

// copyTree 递归复制目录（目标已存在且大小一致则跳过）。
func copyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return copyFile(src, dst)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyTree(s, d); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(s, d); err != nil {
			return err
		}
	}
	return nil
}

// copyFile 复制单个文件，目标存在且大小相同则跳过。
func copyFile(src, dst string) error {
	si, err := os.Stat(src)
	if err != nil {
		return err
	}
	if di, err := os.Stat(dst); err == nil && di.Size() == si.Size() {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

// dirSize 统计目录内文件总大小。
func dirSize(root string) int64 {
	var total int64
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// packEntry 把单个文件写进 zip，读不了就跳过（返回是否成功）。
func packEntry(zw *zip.Writer, path, name string) bool {
	in, err := os.Open(path)
	if err != nil {
		return false
	}
	defer in.Close()
	w, err := zw.Create(name)
	if err != nil {
		return false
	}
	if _, err := io.Copy(w, in); err != nil {
		return false
	}
	return true
}

// zipWalk 统一的打包遍历：跳过读不了的文件和输出文件自身，并回报进度。
func zipWalk(srcDir, zipPath, prefix string, onProgress func(done, total int64), onWarn func(string)) error {
	if fi, err := os.Stat(srcDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("源目录不存在或不是文件夹: %s", srcDir)
	}
	if err := os.MkdirAll(filepath.Dir(zipPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	defer f.Close()

	total := dirSize(srcDir)
	var done int64
	added := 0
	skipPath, _ := filepath.Abs(zipPath)
	skipped := 0

	zw := zip.NewWriter(f)
	walkErr := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// 单个文件出错（被占用/权限不足）不影响整体
			skipped++
			return nil
		}
		if info == nil || info.IsDir() {
			return nil
		}
		abs, _ := filepath.Abs(path)
		if abs == skipPath {
			return nil // 不把正在写的 zip 打进去
		}
		rel, rerr := filepath.Rel(srcDir, path)
		if rerr != nil {
			skipped++
			return nil
		}
		name := prefix + filepath.ToSlash(rel)
		if packEntry(zw, path, name) {
			added++
		} else {
			skipped++
		}
		done += info.Size()
		if onProgress != nil {
			onProgress(done, total)
		}
		return nil
	})
	if walkErr != nil {
		zw.Close()
		os.Remove(zipPath)
		return walkErr
	}
	if err := zw.Close(); err != nil {
		os.Remove(zipPath)
		return err
	}
	if added == 0 {
		os.Remove(zipPath)
		return fmt.Errorf("没有可打包的文件（源目录是空的？）: %s", srcDir)
	}
	if onProgress != nil {
		onProgress(total, total)
	}
	if skipped > 0 && onWarn != nil {
		onWarn(fmt.Sprintf("%d 个文件被占用/无权读取，已跳过（不影响使用）", skipped))
	}
	return nil
}

// exportZip 把目录原样打包成 zip。
func exportZip(srcDir, zipPath string, onProgress func(done, total int64), onWarn func(string)) error {
	return zipWalk(srcDir, zipPath, "", onProgress, onWarn)
}

// exportModpackZip 打包成可被 PCL 导入的整合包（zip 内是 .minecraft/ 目录）。
func exportModpackZip(srcDir, zipPath string, onProgress func(done, total int64), onWarn func(string)) error {
	return zipWalk(srcDir, zipPath, ".minecraft/", onProgress, onWarn)
}

// launcherPreset 描述一个可以作为下载目标的启动器。
type launcherPreset struct {
	ID     string
	Name   string
	Dir    string // 游戏目录（standard/pcl）或 instances 目录（prism）
	Layout string
	Hint   string
}

// appDataDir 返回 %APPDATA%。
func appDataDir() string {
	if v := os.Getenv("APPDATA"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "AppData", "Roaming")
}

// knownLaunchers 返回内置的启动器预设（目录尽量用真实探测结果）。
func knownLaunchers() []launcherPreset {
	appdata := appDataDir()
	mc := filepath.Join(appdata, ".minecraft")

	pclDir := mc
	if _, dir, ok := detectPCL(); ok && dir != "" {
		pclDir = dir
	}
	hmclDir := mc
	if _, dir, ok := detectHMCL(); ok && dir != "" {
		hmclDir = dir
	}
	prismDir := filepath.Join(appdata, "PrismLauncher", "instances")
	if dir, ok := detectPrism(); ok {
		prismDir = dir
	}
	multimcDir := filepath.Join(appdata, "MultiMC", "instances")
	if dir, ok := detectMultiMC(); ok {
		multimcDir = dir
	}
	return []launcherPreset{
		{
			ID: "pcl", Name: "PCL 2/3", Dir: pclDir, Layout: layoutPCL,
			Hint: "PCL 里把「游戏目录」指向这个文件夹，列表里立刻能看到下载好的版本",
		},
		{
			ID: "hmcl", Name: "HMCL", Dir: hmclDir, Layout: layoutStandard,
			Hint: "HMCL 里把「游戏目录」设为这个文件夹",
		},
		{
			ID: "prism", Name: "Prism Launcher", Dir: prismDir, Layout: layoutPrism,
			Hint: "Prism 的 instances 目录，每个版本一个实例文件夹",
		},
		{
			ID: "multimc", Name: "MultiMC", Dir: multimcDir, Layout: layoutPrism,
			Hint: "MultiMC 的 instances 目录",
		},
		{
			ID: "mojang", Name: "官方启动器", Dir: mc, Layout: layoutStandard,
			Hint: "官方启动器默认就读这个目录",
		},
	}
}

// detectPrism 探测 Prism Launcher 的 instances 目录。
// 数据目录默认 %APPDATA%\PrismLauncher，可在 prismlauncher.cfg 里改 InstanceDir。
func detectPrism() (string, bool) {
	appdata := appDataDir()
	dirs := []string{filepath.Join(appdata, "PrismLauncher")}
	if v := os.Getenv("PRISMLAUNCHER_DATA_DIR"); v != "" {
		dirs = append([]string{v}, dirs...)
	}
	for _, d := range dirs {
		if !dirExists(d) {
			continue
		}
		inst := readCFGValue(filepath.Join(d, "prismlauncher.cfg"), "InstanceDir")
		if inst == "" {
			inst = "instances"
		}
		if !filepath.IsAbs(inst) {
			inst = filepath.Join(d, inst)
		}
		return inst, true
	}
	return "", false
}

// detectMultiMC 探测 MultiMC 的 instances 目录。
func detectMultiMC() (string, bool) {
	appdata := appDataDir()
	dirs := []string{filepath.Join(appdata, "MultiMC")}
	if v := os.Getenv("MULTIMC_DATA_DIR"); v != "" {
		dirs = append([]string{v}, dirs...)
	}
	for _, d := range dirs {
		if !dirExists(d) {
			continue
		}
		inst := readCFGValue(filepath.Join(d, "multimc.cfg"), "InstanceDir")
		if inst == "" {
			inst = "instances"
		}
		if !filepath.IsAbs(inst) {
			inst = filepath.Join(d, inst)
		}
		return inst, true
	}
	return "", false
}

// readCFGValue 从 ini 风格配置文件里读一个值（Key=Value）。
func readCFGValue(path, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if len(line) > len(key) && strings.EqualFold(line[:len(key)+1], key+"=") {
			return strings.Trim(strings.TrimSpace(line[len(key)+1:]), "\"")
		}
	}
	return ""
}

// detectHMCL 探测 HMCL 的安装位置与游戏目录。
// HMCL 3.16 之前把 gameDir 写在 hmcl.json / .hmcl.json 里。
func detectHMCL() (exeDir string, gameDir string, ok bool) {
	for _, dir := range findHMCLDirs() {
		for _, name := range []string{"hmcl.json", ".hmcl.json"} {
			p := filepath.Join(dir, name)
			if gd := readHMCLGameDir(p); gd != "" {
				return dir, gd, true
			}
		}
	}
	return "", "", false
}

// findHMCLDirs 在常见位置找 HMCL 所在目录。
func findHMCLDirs() []string {
	home, _ := os.UserHomeDir()
	roots := []string{
		filepath.Join(home, "Desktop"),
		filepath.Join(home, "OneDrive", "Desktop"),
		filepath.Join(home, "Downloads"),
		filepath.Join(home, "AppData", "Local", "Programs"),
		appDataDir() + `\HMCL`,
	}
	for _, d := range []string{"C", "D", "E", "F", "G"} {
		if dirExists(d + `:\`) {
			roots = append(roots, d+`:\`)
		}
	}
	seen := map[string]bool{}
	var out []string
	add := func(dir string) {
		if dir != "" && !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	for _, root := range roots {
		if !dirExists(root) {
			continue
		}
		// 目录本身就有 hmcl.json
		if fileExists(filepath.Join(root, "hmcl.json")) || fileExists(filepath.Join(root, ".hmcl.json")) {
			add(root)
		}
		// 同目录的 HMCL 主程序
		matches, _ := filepath.Glob(filepath.Join(root, "HMCL*.jar"))
		if len(matches) > 0 {
			add(root)
		}
		matches, _ = filepath.Glob(filepath.Join(root, "HMCL*.exe"))
		if len(matches) > 0 {
			add(root)
		}
		// 一层子目录
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			sub := filepath.Join(root, e.Name())
			if fileExists(filepath.Join(sub, "hmcl.json")) || fileExists(filepath.Join(sub, ".hmcl.json")) {
				add(sub)
				continue
			}
			jm, _ := filepath.Glob(filepath.Join(sub, "HMCL*.jar"))
			em, _ := filepath.Glob(filepath.Join(sub, "HMCL*.exe"))
			if len(jm) > 0 || len(em) > 0 {
				add(sub)
			}
		}
	}
	return out
}

// readHMCLGameDir 从 hmcl.json 里读出游戏目录。
func readHMCLGameDir(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var doc map[string]any
	if err := jsonUnmarshal(data, &doc); err != nil {
		return ""
	}
	// 旧格式：字符串
	if v, ok := doc["gameDir"].(string); ok && v != "" {
		return normalizePath(v, filepath.Dir(path))
	}
	// 新格式：gameDirectories: [{"path": "..."}]
	if arr, ok := doc["gameDirectories"].([]any); ok && len(arr) > 0 {
		if m, ok := arr[0].(map[string]any); ok {
			if v, ok := m["path"].(string); ok && v != "" {
				return normalizePath(v, filepath.Dir(path))
			}
		}
	}
	return ""
}

// normalizePath 处理 "~" 与相对路径。
func normalizePath(p, base string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "~") {
		home, _ := os.UserHomeDir()
		p = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), `\`))
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return p
}

// detectPCL 寻找 PCL 安装目录，并读出它配置的游戏目录。
// PCL 2 把选中的游戏目录写在 <PCL目录>\PCL\Setup.ini 的 LaunchFolderSelect 里。
func detectPCL() (exeDir string, gameDir string, ok bool) {
	for _, exe := range findPCLExe() {
		dir := filepath.Dir(exe)
		game := readPCLGameDir(dir)
		if game == "" {
			game = filepath.Join(dir, ".minecraft")
		}
		return dir, game, true
	}
	return "", "", false
}

// findPCLExe 在常见位置寻找 PCL 主程序。
func findPCLExe() []string {
	names := []string{"Plain Craft Launcher 2.exe", "PCL.exe", "PCL3.exe"}
	var roots []string
	home, _ := os.UserHomeDir()
	roots = append(roots,
		filepath.Join(home, "Desktop"),
		filepath.Join(home, "OneDrive", "Desktop"),
		filepath.Join(appDataDir(), "Microsoft", "Windows", "Start Menu", "Programs"),
		filepath.Join(home, "AppData", "Local", "Programs"),
	)
	// 各盘根目录下一层
	for _, d := range []string{"C", "D", "E", "F", "G"} {
		if dirExists(d + `:\`) {
			roots = append(roots, d+`:\`)
		}
	}
	var out []string
	for _, root := range roots {
		if !dirExists(root) {
			continue
		}
		for _, name := range names {
			matches, _ := filepath.Glob(filepath.Join(root, name))
			for _, m := range matches {
				out = append(out, m)
			}
		}
		// 一层子目录（很多 PCL 放在某个文件夹里）
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			for _, name := range names {
				p := filepath.Join(root, e.Name(), name)
				if fileExists(p) {
					out = append(out, p)
				}
			}
		}
	}
	return out
}

// readPCLGameDir 读 PCL 的 Setup.ini，取出选中的游戏目录。
func readPCLGameDir(exeDir string) string {
	data, err := os.ReadFile(filepath.Join(exeDir, "PCL", "Setup.ini"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToLower(line), "launchfolderselect:") {
			continue
		}
		v := strings.TrimSpace(line[len("launchfolderselect:"):])
		v = strings.Trim(v, "\"")
		if v == "" {
			return ""
		}
		if strings.HasPrefix(v, "$") {
			// 相对 PCL 目录
			return filepath.Join(exeDir, strings.TrimPrefix(v, "$"))
		}
		return v
	}
	return ""
}

// fileExists 判断文件是否存在。
func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// launcherByID 按 ID 找预设。
func launcherByID(id string) (launcherPreset, bool) {
	for _, p := range knownLaunchers() {
		if p.ID == id {
			return p, true
		}
	}
	return launcherPreset{}, false
}

// detectLaunchers 返回本机已安装（能检测到）的启动器预设。
func detectLaunchers() []launcherPreset {
	var out []launcherPreset
	appdata := appDataDir()
	if _, dir, ok := detectPCL(); ok {
		out = append(out, launcherPreset{
			ID: "pcl", Name: "PCL 2/3", Dir: dir, Layout: layoutPCL,
			Hint: "已自动读取 PCL 配置的游戏目录",
		})
	}
	if _, dir, ok := detectHMCL(); ok {
		out = append(out, launcherPreset{
			ID: "hmcl", Name: "HMCL", Dir: dir, Layout: layoutStandard,
			Hint: "已自动读取 HMCL 配置的游戏目录",
		})
	}
	if dir, ok := detectPrism(); ok {
		out = append(out, launcherPreset{
			ID: "prism", Name: "Prism Launcher", Dir: dir, Layout: layoutPrism,
			Hint: "已自动读取 Prism 的 instances 目录",
		})
	}
	if dir, ok := detectMultiMC(); ok {
		out = append(out, launcherPreset{
			ID: "multimc", Name: "MultiMC", Dir: dir, Layout: layoutPrism,
			Hint: "已自动读取 MultiMC 的 instances 目录",
		})
	}
	mc := filepath.Join(appdata, ".minecraft")
	if dirExists(filepath.Join(mc, "versions")) {
		out = append(out, launcherPreset{
			ID: "mojang", Name: "官方启动器", Dir: mc, Layout: layoutStandard,
			Hint: "官方启动器的默认游戏目录",
		})
	}
	return out
}

// pickBestLauncher 按优先级挑一个已检测到的启动器（PCL > HMCL > Prism > MultiMC > 官方）。
func pickBestLauncher() (launcherPreset, bool) {
	list := detectLaunchers()
	order := []string{"pcl", "hmcl", "prism", "multimc", "mojang"}
	for _, id := range order {
		for _, p := range list {
			if p.ID == id {
				return p, true
			}
		}
	}
	return launcherPreset{}, false
}

// describeDetected 返回检测结果的一句话描述。
func describeDetected() string {
	list := detectLaunchers()
	if len(list) == 0 {
		return ""
	}
	parts := make([]string, 0, len(list))
	for _, p := range list {
		parts = append(parts, p.Name+"("+p.Dir+")")
	}
	return strings.Join(parts, "  ")
}

// dirExists 判断目录是否存在。
func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// writeLocalManifest 导出「已下载版本清单」。
// PCL 的启动列表本来就等于本地 versions/ 目录，这份清单用于脚本排查或自建镜像。
func (e *Engine) writeLocalManifest() error {
	type entry struct {
		ID          string `json:"id"`
		Type        string `json:"type"`
		ReleaseTime string `json:"releaseTime"`
		OfficialURL string `json:"officialUrl"`
		LocalJSON   string `json:"localJson"`
		ClientJAR   string `json:"clientJar,omitempty"`
		ServerJAR   string `json:"serverJar,omitempty"`
		Complete    bool   `json:"complete"`
	}
	entries := make([]entry, 0, len(e.jobs))
	complete := 0
	for _, j := range e.jobs {
		e := entry{
			ID:          j.ID,
			Type:        j.Type,
			OfficialURL: j.URL,
			LocalJSON:   filepath.ToSlash(filepath.Join("versions", j.ID, j.ID+".json")),
		}
		ok := true
		for _, t := range j.Tasks {
			fi, err := os.Stat(t.Dest)
			if err != nil || (t.Size > 0 && fi.Size() != t.Size) {
				ok = false
				continue
			}
			switch t.Kind {
			case "client":
				e.ClientJAR = filepath.ToSlash(t.Dest)
			case "server":
				e.ServerJAR = filepath.ToSlash(t.Dest)
			}
		}
		e.Complete = ok
		if ok {
			complete++
		}
		entries = append(entries, e)
	}
	doc := map[string]any{
		"generator":   "mcdl",
		"generatedAt": time.Now().Format(time.RFC3339),
		"layout":      e.layout,
		"outputDir":   e.out,
		"downloaded":  complete,
		"total":       len(e.jobs),
		"versions":    entries,
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(e.out, "version_manifest.local.json"), data, 0o644)
}

// humanLayout 返回布局的中文名。
func humanLayout(l string) string {
	switch l {
	case layoutPCL:
		return "PCL 友好"
	case layoutPrism:
		return "Prism/MultiMC 实例"
	default:
		return "标准 .minecraft"
	}
}

// isLayoutName 判断是否是合法的布局名。
func isLayoutName(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, n := range layoutNames {
		if s == n {
			return true
		}
	}
	return false
}

// nextLayout 返回下一个布局（界面里用 ←→ 切换）。
func nextLayout(cur string, delta int) string {
	idx := 0
	for i, n := range layoutNames {
		if n == cur {
			idx = i
			break
		}
	}
	idx = (idx + delta + len(layoutNames)) % len(layoutNames)
	return layoutNames[idx]
}

// writeLauncherProfiles 生成 launcher_profiles.json（官方启动器 / PCL 3 识别游戏目录用）。
func (e *Engine) writeLauncherProfiles() error {
	if !e.cfg.LauncherProfile {
		return nil
	}
	profiles := map[string]any{
		"profiles": map[string]any{
			"mcdl": map[string]any{
				"name":        "mcdl",
				"type":        "custom",
				"created":     "2024-01-01T00:00:00Z",
				"lastUsed":    "2024-01-01T00:00:00Z",
				"lastVersion": e.latestVersionID(),
				"gameDir":     ".",
				"javaDir":     "",
				"javaArgs":    "",
				"resolution":  map[string]any{"width": 854, "height": 480},
			},
		},
		"selectedProfile": "mcdl",
	}
	data, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(e.out, "launcher_profiles.json"), data, 0o644)
}

// latestVersionID 返回最近的一个版本号（给 launcher_profiles 用）。
func (e *Engine) latestVersionID() string {
	for _, j := range e.jobs {
		if j.Type == "release" {
			return j.ID
		}
	}
	if len(e.jobs) > 0 {
		return e.jobs[0].ID
	}
	return ""
}

// extractNatives 把原生库 jar 解包到 natives 目录（部分第三方启动器不会自动做这一步）。
func extractNatives(jarPath, dstDir string, exclude []string) error {
	r, err := zip.OpenReader(jarPath)
	if err != nil {
		return err
	}
	defer r.Close()
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	count := 0
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := filepath.ToSlash(f.Name)
		if isExcluded(name, exclude) {
			continue
		}
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, ".dll") && !strings.HasSuffix(lower, ".so") &&
			!strings.HasSuffix(lower, ".dylib") && !strings.HasSuffix(lower, ".jnilib") {
			continue
		}
		out := filepath.Join(dstDir, filepath.Base(name))
		if err := extractZipFile(f, out); err != nil {
			return err
		}
		count++
	}
	if count == 0 {
		return fmt.Errorf("原生库里没有可解出的动态库")
	}
	return nil
}

// isExcluded 判断路径是否在排除列表里。
func isExcluded(name string, exclude []string) bool {
	for _, ex := range exclude {
		ex = filepath.ToSlash(ex)
		if ex == "" {
			continue
		}
		if strings.HasSuffix(ex, "/") {
			if strings.HasPrefix(name, ex) {
				return true
			}
			continue
		}
		if name == ex {
			return true
		}
	}
	return false
}

// extractZipFile 解出单个文件。
func extractZipFile(f *zip.File, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}
