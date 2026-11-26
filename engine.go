package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var errPaused = errors.New("已暂停")

const (
	statePending int32 = iota
	stateRunning
	stateDone
	stateFailed
	stateSkipped
)

// Task 是单个待下载文件。
type Task struct {
	Kind     string
	Version  string
	Label    string
	URL      string
	Dest     string
	Size     int64
	SHA1     string
	Key      string
	job      *Job
	AltURL   string // 备用源地址（主源失败时切换）
	triedAlt bool
	Native   bool     // 是否是原生库（需要解包）
	Exclude  []string // 解包时排除的路径

	mu       sync.Mutex
	state    int32
	written  int64
	errMsg   string
	attempts int32
}

func (t *Task) State() int32    { return atomic.LoadInt32(&t.state) }
func (t *Task) Written() int64  { return atomic.LoadInt64(&t.written) }
func (t *Task) ErrMsg() string  { t.mu.Lock(); defer t.mu.Unlock(); return t.errMsg }
func (t *Task) setErr(s string) { t.mu.Lock(); t.errMsg = s; t.mu.Unlock() }
func (t *Task) Attempts() int32 { return atomic.LoadInt32(&t.attempts) }

// Progress 返回该任务的完成比例（0~1）。
func (t *Task) Progress() float64 {
	if t.Size <= 0 {
		return 0
	}
	w := t.Written()
	if w > t.Size {
		w = t.Size
	}
	return float64(w) / float64(t.Size)
}

// Job 是一个版本的下载任务集合。
type Job struct {
	ID    string
	Type  string
	URL   string
	Tasks []*Task

	total int64
	done  int64
	live  int64
	fail  int32
	state int32
}

func (j *Job) Total() int64     { return atomic.LoadInt64(&j.total) }
func (j *Job) Done() int64      { return atomic.LoadInt64(&j.done) + atomic.LoadInt64(&j.live) }
func (j *Job) Live() int64      { return atomic.LoadInt64(&j.live) }
func (j *Job) Fail() int32      { return atomic.LoadInt32(&j.fail) }
func (j *Job) State() int32     { return atomic.LoadInt32(&j.state) }
func (j *Job) setState(s int32) { atomic.StoreInt32(&j.state, s) }

// Engine 是下载引擎：负责准备任务清单、并发下载、校验、重试、续传。
type Engine struct {
	cfg    *Config
	out    string
	layout string
	source string
	probe  string

	jobs  []*Job
	tasks []*Task

	totalSize int64
	written   int64

	paused   int32
	resumeCh chan struct{}

	queue  chan *Task
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	logf     func(string)
	progF    func(done, total int, phase string)
	prepared int32
	prepAll  int32
	running  int32

	speedMu    sync.Mutex
	speedBytes int64
	speedTime  time.Time
	speedVal   float64
}

// NewEngine 创建引擎。
func NewEngine(cfg *Config) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	layout := cfg.Layout
	if !isLayoutName(layout) {
		layout = layoutStandard
	}
	return &Engine{
		cfg:       cfg,
		out:       cfg.OutputDir,
		layout:    layout,
		source:    sourceOfficial,
		jobs:      []*Job{},
		tasks:     []*Task{},
		resumeCh:  make(chan struct{}, 1),
		queue:     make(chan *Task, 1024),
		ctx:       ctx,
		cancel:    cancel,
		logf:      func(string) {},
		progF:     func(int, int, string) {},
		speedTime: time.Now(),
	}
}

// Speed 估算当前下载速度（字节/秒）。
func (e *Engine) Speed() float64 {
	e.speedMu.Lock()
	defer e.speedMu.Unlock()
	now := time.Now()
	dt := now.Sub(e.speedTime).Seconds()
	if dt < 0.25 {
		return e.speedVal
	}
	w := atomic.LoadInt64(&e.written)
	inst := float64(w-e.speedBytes) / dt
	if e.speedVal == 0 {
		e.speedVal = inst
	} else {
		e.speedVal = e.speedVal*0.6 + inst*0.4
	}
	e.speedBytes = w
	e.speedTime = now
	return e.speedVal
}

// SetLogger 设置日志回调。
func (e *Engine) SetLogger(f func(string)) {
	if f != nil {
		e.logf = f
	}
}

// SetProgress 设置准备阶段进度回调。
func (e *Engine) SetProgress(f func(done, total int, phase string)) {
	if f != nil {
		e.progF = f
	}
}

// OutputDir 返回输出目录。
func (e *Engine) OutputDir() string { return e.out }

// Jobs 返回全部任务组。
func (e *Engine) Jobs() []*Job { return e.jobs }

// Tasks 返回全部下载任务。
func (e *Engine) Tasks() []*Task { return e.tasks }

// TotalSize 返回计划下载的总字节数。
func (e *Engine) TotalSize() int64 { return atomic.LoadInt64(&e.totalSize) }

// Written 返回已写入的字节数。
func (e *Engine) Written() int64 { return atomic.LoadInt64(&e.written) }

// Preparing 返回当前准备进度。
func (e *Engine) Preparing() (int, int) {
	return int(atomic.LoadInt32(&e.prepared)), int(atomic.LoadInt32(&e.prepAll))
}

// Source 返回当前使用的下载源。
func (e *Engine) Source() string { return e.source }

// ProbeInfo 返回测速说明。
func (e *Engine) ProbeInfo() string { return e.probe }

// Prepare 拉取所有选中版本的元数据，生成下载计划。
func (e *Engine) Prepare(ctx context.Context, entries []ManifestEntry) error {
	// 先定下载源：auto 就先测速
	switch {
	case e.cfg.Source == sourceCustom:
		e.source = sourceCustom
		e.logf("下载源：自定义镜像")
	case e.cfg.Source != sourceAuto:
		if d, ok := sourceByID(e.cfg.Source); ok {
			e.source = d.ID
			e.logf("下载源：" + d.Name)
		} else {
			e.source = sourceOfficial
		}
	default:
		e.logf("正在测速选择下载源…")
		best, results := probeBestSource()
		e.source = best
		e.probe = describeProbe(results)
		e.logf("测速完成：" + e.probe + " → 使用 " + sourceLabel(best))
	}

	atomic.StoreInt32(&e.prepAll, int32(len(entries)))
	atomic.StoreInt32(&e.prepared, 0)

	infos := make([]*VersionInfo, len(entries))
	errs := make([]error, len(entries))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 16)
	for i := range entries {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			info, err := e.loadVersionInfo(ctx, entries[i])
			infos[i], errs[i] = info, err
			atomic.AddInt32(&e.prepared, 1)
			e.progF(int(atomic.LoadInt32(&e.prepared)), len(entries), "读取版本信息")
		}(i)
	}
	wg.Wait()

	seen := map[string]*Task{}
	for i, entry := range entries {
		job := &Job{ID: entry.ID, Type: entry.Type, URL: entry.URL}
		e.jobs = append(e.jobs, job)

		if errs[i] != nil || infos[i] == nil {
			job.setState(stateFailed)
			atomic.AddInt32(&job.fail, 1)
			e.logf(fmt.Sprintf("[x] %s 元数据获取失败: %v", entry.ID, errs[i]))
			continue
		}
		info := infos[i]
		game := e.gameDir(entry.ID)

		// 版本 JSON（官方启动器需要它来识别版本）
		jsonPath := filepath.Join(game, "versions", entry.ID, entry.ID+".json")
		jsonURL, jsonAlt := e.taskURLs("json", entry.ID, entry.URL, "", "")
		addTask(e, job, seen, &Task{
			Kind: "json", Version: entry.ID, Label: "版本信息",
			URL: jsonURL, AltURL: jsonAlt, Dest: jsonPath,
			Size: -1, Key: "json:" + entry.ID,
		})

		if e.cfg.DownloadClient {
			if f, ok := info.Downloads["client"]; ok && f.URL != "" {
				u, a := e.taskURLs("client", entry.ID, f.URL, "", "")
				addTask(e, job, seen, &Task{
					Kind: "client", Version: entry.ID, Label: "客户端",
					URL: u, AltURL: a,
					Dest: filepath.Join(game, "versions", entry.ID, entry.ID+".jar"),
					Size: f.Size, SHA1: f.SHA1, Key: "client:" + entry.ID,
				})
			}
		}
		if e.cfg.DownloadServer {
			if f, ok := info.Downloads["server"]; ok && f.URL != "" {
				u, a := e.taskURLs("server", entry.ID, f.URL, "", "")
				addTask(e, job, seen, &Task{
					Kind: "server", Version: entry.ID, Label: "服务端",
					URL: u, AltURL: a,
					Dest: filepath.Join(game, "versions", entry.ID, "server.jar"),
					Size: f.Size, SHA1: f.SHA1, Key: "server:" + entry.ID,
				})
			}
		}
		if e.cfg.DownloadMapping {
			if f, ok := info.Downloads["client_mappings"]; ok && f.URL != "" {
				addTask(e, job, seen, &Task{
					Kind: "mapping", Version: entry.ID, Label: "客户端映射",
					URL:  applyMirrors(f.URL, e.cfg),
					Dest: filepath.Join(game, "versions", entry.ID, "client.txt"),
					Size: f.Size, SHA1: f.SHA1, Key: "mapc:" + entry.ID,
				})
			}
			if f, ok := info.Downloads["server_mappings"]; ok && f.URL != "" {
				addTask(e, job, seen, &Task{
					Kind: "mapping", Version: entry.ID, Label: "服务端映射",
					URL:  applyMirrors(f.URL, e.cfg),
					Dest: filepath.Join(game, "versions", entry.ID, "server.txt"),
					Size: f.Size, SHA1: f.SHA1, Key: "maps:" + entry.ID,
				})
			}
		}
		if e.cfg.DownloadLibs {
			e.addLibraryTasks(job, seen, info)
		}
		if e.cfg.DownloadAssets && info.AssetIndex != nil {
			e.addAssetTasks(ctx, job, seen, info)
		}
	}
	return nil
}

// taskURLs 按当前下载源给出主地址与备用地址。
func (e *Engine) taskURLs(kind, versionID, officialURL, mavenPath, hash string) (primary, alt string) {
	official := applyMirrors(officialURL, e.cfg)
	base := ""
	if e.source != sourceCustom {
		if d, ok := sourceByID(e.source); ok {
			base = d.Base
		}
	}
	if base == "" {
		return official, ""
	}
	if u, ok := mirrorURL(base, kind, versionID, officialURL, mavenPath, hash); ok {
		return applyMirrors(u, e.cfg), official
	}
	return official, ""
}

// addTask 把任务加入全局列表（按 Key 去重，重复的只统计一次体积）。
func addTask(e *Engine, job *Job, seen map[string]*Task, t *Task) {
	if t.Key == "" {
		t.Key = t.Dest
	}
	if _, ok := seen[t.Key]; ok {
		return
	}
	seen[t.Key] = t
	e.tasks = append(e.tasks, t)
	job.Tasks = append(job.Tasks, t)
	t.job = job
	if t.Size > 0 {
		atomic.AddInt64(&job.total, t.Size)
		atomic.AddInt64(&e.totalSize, t.Size)
	}
}

// addLibraryTasks 为版本添加依赖库下载任务。
func (e *Engine) addLibraryTasks(job *Job, seen map[string]*Task, info *VersionInfo) {
	for _, lib := range info.Libraries {
		if !rulesAllow(lib.Rules) {
			continue
		}
		if skipNativeByArch(libClassifier(lib.Name)) {
			continue
		}
		if lib.Downloads.Artifact != nil && lib.Downloads.Artifact.URL != "" {
			f := lib.Downloads.Artifact
			u, a := e.taskURLs("library", info.ID, f.URL, f.Path, "")
			addTask(e, job, seen, &Task{
				Kind: "library", Version: info.ID, Label: "库 " + lib.Name,
				URL: u, AltURL: a,
				Dest: filepath.Join(e.sharedLibrariesDir(), filepath.FromSlash(f.Path)),
				Size: f.Size, SHA1: f.SHA1, Key: "lib:" + f.Path,
			})
		}
		// 老格式：原生库按 classifier 选
		if len(lib.Downloads.Classifiers) > 0 && lib.Natives != nil {
			classifier := ""
			switch currentOSName() {
			case "windows":
				classifier = lib.Natives["windows"]
			case "osx":
				classifier = lib.Natives["osx"]
			case "linux":
				classifier = lib.Natives["linux"]
			}
			if classifier == "" {
				continue
			}
			if f, ok := lib.Downloads.Classifiers[classifier]; ok && f != nil && f.URL != "" {
				u, a := e.taskURLs("native", info.ID, f.URL, f.Path, "")
				addTask(e, job, seen, &Task{
					Kind: "native", Version: info.ID, Label: "原生库 " + lib.Name,
					URL: u, AltURL: a,
					Dest: filepath.Join(e.sharedLibrariesDir(), filepath.FromSlash(f.Path)),
					Size: f.Size, SHA1: f.SHA1, Key: "lib:" + f.Path,
					Native: true, Exclude: libExclude(lib),
				})
			}
		}
	}
}

// libExclude 取依赖库的解包排除列表。
func libExclude(lib Library) []string {
	if lib.Extract == nil {
		return nil
	}
	return lib.Extract.Exclude
}

// addAssetTasks 为版本添加资源（assets）下载任务。
func (e *Engine) addAssetTasks(ctx context.Context, job *Job, seen map[string]*Task, info *VersionInfo) {
	idx := info.AssetIndex
	if idx == nil || idx.URL == "" {
		return
	}
	indexPath := filepath.Join(e.sharedAssetsDir(), "indexes", idx.ID+".json")
	addTask(e, job, seen, &Task{
		Kind: "assetindex", Version: info.ID, Label: "资源索引 " + idx.ID,
		URL: applyMirrors(idx.URL, e.cfg), Dest: indexPath,
		Size: idx.Size, SHA1: idx.SHA1, Key: "assetindex:" + idx.ID,
	})

	var content AssetIndex
	if data, err := os.ReadFile(indexPath); err == nil {
		_ = decodeAssetIndex(data, &content)
	} else {
		data, err := fetchBytes(ctx, applyMirrors(idx.URL, e.cfg))
		if err != nil {
			e.logf(fmt.Sprintf("[x] %s 资源索引下载失败: %v", info.ID, err))
			return
		}
		if err := decodeAssetIndex(data, &content); err != nil {
			e.logf(fmt.Sprintf("[x] %s 资源索引解析失败: %v", info.ID, err))
			return
		}
	}
	for name, obj := range content.Objects {
		if obj.Hash == "" {
			continue
		}
		u, a := e.taskURLs("asset", info.ID, resourcePrefix+obj.Hash[:2]+"/"+obj.Hash, "", obj.Hash)
		addTask(e, job, seen, &Task{
			Kind: "asset", Version: info.ID, Label: "资源 " + name,
			URL: u, AltURL: a,
			Dest: filepath.Join(e.sharedAssetsDir(), "objects", obj.Hash[:2], obj.Hash),
			Size: obj.Size, Key: "asset:" + obj.Hash,
		})
	}
}

func decodeAssetIndex(data []byte, out *AssetIndex) error {
	return jsonUnmarshal(data, out)
}

// loadVersionInfo 获取版本元数据，优先复用本地已下载的 JSON。
func (e *Engine) loadVersionInfo(ctx context.Context, entry ManifestEntry) (*VersionInfo, error) {
	local := filepath.Join(e.gameDir(entry.ID), "versions", entry.ID, entry.ID+".json")
	if data, err := os.ReadFile(local); err == nil && len(data) > 0 {
		var info VersionInfo
		if err := jsonUnmarshal(data, &info); err == nil && info.ID != "" {
			return &info, nil
		}
	}
	data, err := fetchBytes(ctx, applyMirrors(entry.URL, e.cfg))
	if err != nil {
		return nil, err
	}
	var info VersionInfo
	if err := jsonUnmarshal(data, &info); err != nil {
		return nil, err
	}
	if info.ID == "" {
		info.ID = entry.ID
	}
	_ = writeFileAtomic(local, data)
	return &info, nil
}

// Run 启动工作协程下载所有任务。
func (e *Engine) Run() {
	atomic.StoreInt32(&e.running, 1)
	workers := e.cfg.Concurrency
	if workers < 1 {
		workers = 1
	}
	for i := 0; i < workers; i++ {
		e.wg.Add(1)
		go e.worker()
	}
	go func() {
		defer close(e.queue)
		for _, t := range e.tasks {
			select {
			case e.queue <- t:
			case <-e.ctx.Done():
				return
			}
		}
	}()
}

func (e *Engine) worker() {
	defer e.wg.Done()
	for {
		select {
		case <-e.ctx.Done():
			return
		case t, ok := <-e.queue:
			if !ok {
				return
			}
			if err := e.waitIfPaused(); err != nil {
				return
			}
			e.runTask(t)
		}
	}
}

// runTask 执行单个任务，返回是否最终成功。
func (e *Engine) runTask(t *Task) bool {
	maxTry := e.cfg.Retry + 1
	for attempt := int32(1); attempt <= int32(maxTry); attempt++ {
		atomic.StoreInt32(&t.attempts, attempt)
		atomic.StoreInt32(&t.state, stateRunning)
		err := e.download(t)
		if err == nil {
			if t.Native && e.cfg.ExtractNatives {
				if xerr := extractNatives(t.Dest, e.nativesDir(t.Version), t.Exclude); xerr != nil {
					e.logf(fmt.Sprintf("[!] %s 原生库解包失败: %v", t.Version, xerr))
				}
			}
			atomic.StoreInt32(&t.state, stateDone)
			e.markJobDone(t)
			return true
		}
		if errors.Is(err, errPaused) {
			atomic.StoreInt32(&t.state, statePending)
			if werr := e.waitIfPaused(); werr != nil {
				return false
			}
			continue
		}
		if errors.Is(err, context.Canceled) {
			atomic.StoreInt32(&t.state, stateFailed)
			t.setErr("已取消")
			e.markJobFail(t)
			return false
		}
		t.setErr(err.Error())
		atomic.StoreInt32(&t.state, statePending)
		e.logf(fmt.Sprintf("[!] %s %s 下载失败(第%d次): %v", t.Version, t.Label, attempt, err))
		if attempt < int32(maxTry) {
			back := time.Duration(attempt) * time.Second
			select {
			case <-e.ctx.Done():
				return false
			case <-time.After(back):
			}
		}
	}
	// 主源彻底失败，切备用源再试一轮
	if t.AltURL != "" && !t.triedAlt {
		t.triedAlt = true
		t.URL, t.AltURL = t.AltURL, t.URL
		e.logf(fmt.Sprintf("[~] %s %s 切换备用源重试", t.Version, t.Label))
		atomic.StoreInt32(&t.state, statePending)
		return e.runTask(t)
	}
	atomic.StoreInt32(&t.state, stateFailed)
	e.markJobFail(t)
	return false
}

func (e *Engine) markJobDone(t *Task) {
	size := t.Written()
	if t.Size > 0 {
		size = t.Size
	}
	if t.job != nil {
		atomic.AddInt64(&t.job.done, size)
		atomic.AddInt64(&t.job.live, -t.Written())
	}
}

func (e *Engine) markJobFail(t *Task) {
	if t.job != nil {
		atomic.AddInt32(&t.job.fail, 1)
	}
}

// download 执行单个文件下载（支持续传、校验、重试）。
func (e *Engine) download(t *Task) error {
	// 已存在且大小吻合则跳过
	if e.cfg.SkipExisting {
		if fi, err := os.Stat(t.Dest); err == nil {
			if t.Size > 0 && fi.Size() == t.Size {
				if !e.cfg.VerifySHA1 || t.SHA1 == "" {
					atomic.StoreInt64(&t.written, fi.Size())
					atomic.AddInt64(&e.written, fi.Size())
					return nil
				}
				if got, err := sha1File(t.Dest); err == nil && strings.EqualFold(got, t.SHA1) {
					atomic.StoreInt64(&t.written, fi.Size())
					atomic.AddInt64(&e.written, fi.Size())
					return nil
				}
			}
		}
	}

	if err := os.MkdirAll(filepath.Dir(t.Dest), 0o755); err != nil {
		return err
	}
	part := t.Dest + ".part"
	var offset int64
	if e.cfg.Resume {
		if fi, err := os.Stat(part); err == nil {
			offset = fi.Size()
		}
	}
	if offset > 0 {
		atomic.StoreInt64(&t.written, offset)
		atomic.AddInt64(&e.written, offset)
	}

	req, err := http.NewRequestWithContext(e.ctx, http.MethodGet, t.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		if offset > 0 {
			// 服务器忽略了 Range，从头开始
			offset = 0
		}
	case http.StatusPartialContent:
		// 续传
	case http.StatusRequestedRangeNotSatisfiable:
		if t.Size > 0 {
			if fi, err := os.Stat(part); err == nil && fi.Size() == t.Size {
				_ = os.Rename(part, t.Dest)
				atomic.StoreInt64(&t.written, fi.Size())
				atomic.AddInt64(&e.written, fi.Size())
				return nil
			}
		}
		_ = os.Remove(part)
		return fmt.Errorf("HTTP 416（本地文件不完整，已重置）")
	default:
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// 206 是续传（追加），200 是重新下载（覆盖）
	flags := os.O_CREATE | os.O_WRONLY
	if resp.StatusCode == http.StatusPartialContent && offset > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
		offset = 0
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	written := offset
	buf := make([]byte, 512*1024)
	for {
		if atomic.LoadInt32(&e.paused) == 1 {
			f.Close()
			return errPaused
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return werr
			}
			written += int64(n)
			atomic.StoreInt64(&t.written, written)
			atomic.AddInt64(&e.written, int64(n))
			if t.job != nil {
				atomic.AddInt64(&t.job.live, int64(n))
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return rerr
		}
	}
	if err := f.Close(); err != nil {
		return err
	}

	if t.Size > 0 {
		if fi, err := os.Stat(part); err == nil && fi.Size() != t.Size {
			_ = os.Remove(part)
			return fmt.Errorf("大小不符：期望 %d 字节，实际 %d 字节", t.Size, fi.Size())
		}
	}
	if e.cfg.VerifySHA1 && t.SHA1 != "" {
		got, err := sha1File(part)
		if err != nil {
			return err
		}
		if !strings.EqualFold(got, t.SHA1) {
			_ = os.Remove(part)
			return fmt.Errorf("SHA1 校验失败：期望 %s，实际 %s", t.SHA1, got)
		}
	}
	if err := os.Rename(part, t.Dest); err != nil {
		return err
	}
	atomic.StoreInt64(&t.written, written)
	return nil
}

// Pause 暂停下载。
func (e *Engine) Pause() { atomic.StoreInt32(&e.paused, 1) }

// Resume 恢复下载。
func (e *Engine) Resume() {
	if atomic.CompareAndSwapInt32(&e.paused, 1, 0) {
		select {
		case e.resumeCh <- struct{}{}:
		default:
		}
	}
}

// Paused 返回是否处于暂停状态。
func (e *Engine) Paused() bool { return atomic.LoadInt32(&e.paused) == 1 }

// Cancel 取消全部下载。
func (e *Engine) Cancel() { e.cancel() }

// Wait 等待全部下载结束。
func (e *Engine) Wait() { e.wg.Wait() }

// Finalize 下载完成后的收尾：写 instance.cfg / launcher_profiles、分发共享资源。
func (e *Engine) Finalize(versionIDs []string) error {
	if e.layout == layoutPrism {
		for _, vid := range versionIDs {
			if err := e.writeInstanceCfg(vid, vid); err != nil {
				return err
			}
		}
		if err := e.distributeShared(versionIDs); err != nil {
			return err
		}
	}
	if err := e.writeLauncherProfiles(); err != nil {
		e.logf("[!] 生成 launcher_profiles.json 失败: " + err.Error())
	}
	if err := e.writeLocalManifest(); err != nil {
		e.logf("[!] 导出已下载清单失败: " + err.Error())
	}
	return nil
}

func (e *Engine) waitIfPaused() error {
	for {
		if atomic.LoadInt32(&e.paused) != 1 {
			return nil
		}
		select {
		case <-e.ctx.Done():
			return e.ctx.Err()
		case <-e.resumeCh:
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// sha1File 计算文件 SHA1。
func sha1File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
