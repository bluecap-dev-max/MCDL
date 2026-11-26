package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	manifestURL = "https://launchermeta.mojang.com/mc/game/version_manifest_v2.json"

	pistonMetaPrefix   = "https://piston-meta.mojang.com/v1/packages/"
	launcherMetaPrefix = "https://launchermeta.mojang.com/v1/packages/"
	pistonDataPrefix   = "https://piston-data.mojang.com/v1/objects/"
	launcherDataPrefix = "https://launcher.mojang.com/v1/objects/"
	resourcePrefix     = "https://resources.download.minecraft.net/"
	libraryPrefix      = "https://libraries.minecraft.net/"

	// userAgent 不能带 example.invalid 之类字样，BMCLAPI 会直接 403。
	userAgent = "mcdl/1.0 (Minecraft version downloader)"
)

// 大文件下载客户端：不设整体超时，靠 ResponseHeaderTimeout + 上下文控制。
var httpClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 25 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   25 * time.Second,
		ExpectContinueTimeout: 5 * time.Second,
		ResponseHeaderTimeout: 40 * time.Second,
		ForceAttemptHTTP2:     true,
	},
}

// 小 JSON 请求客户端：整体超时 60 秒。
var apiClient = &http.Client{
	Timeout:   60 * time.Second,
	Transport: httpClient.Transport,
}

// Manifest 是官方版本清单。
type Manifest struct {
	Latest struct {
		Release  string `json:"release"`
		Snapshot string `json:"snapshot"`
	} `json:"latest"`
	Versions []ManifestEntry `json:"versions"`
}

// ManifestEntry 是清单里的单个版本条目。
type ManifestEntry struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	URL         string `json:"url"`
	Time        string `json:"time"`
	ReleaseTime string `json:"releaseTime"`
}

// VersionInfo 是单个版本的元数据 JSON（只取需要的字段）。
type VersionInfo struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	MainClass   string            `json:"mainClass"`
	Assets      string            `json:"assets"`
	AssetIndex  *AssetIndexInfo   `json:"assetIndex"`
	Downloads   map[string]DlFile `json:"downloads"`
	Libraries   []Library         `json:"libraries"`
	JavaVersion struct {
		Component    string `json:"component"`
		MajorVersion int    `json:"majorVersion"`
	} `json:"javaVersion"`
	ReleaseTime string `json:"releaseTime"`
	Time        string `json:"time"`
}

// DlFile 是待下载文件的元信息。
type DlFile struct {
	SHA1 string `json:"sha1"`
	Size int64  `json:"size"`
	URL  string `json:"url"`
	Path string `json:"path"`
}

// AssetIndexInfo 描述资源索引。
type AssetIndexInfo struct {
	ID        string `json:"id"`
	SHA1      string `json:"sha1"`
	Size      int64  `json:"size"`
	TotalSize int64  `json:"totalSize"`
	URL       string `json:"url"`
}

// AssetIndex 是资源索引文件的内容。
type AssetIndex struct {
	Objects        map[string]AssetObject `json:"objects"`
	Virtual        bool                   `json:"virtual"`
	MapToResources bool                   `json:"map_to_resources"`
}

// AssetObject 是单个资源对象。
type AssetObject struct {
	Hash string `json:"hash"`
	Size int64  `json:"size"`
}

// Library 是版本依赖库条目。
type Library struct {
	Name      string `json:"name"`
	Downloads struct {
		Artifact    *DlFile            `json:"artifact"`
		Classifiers map[string]*DlFile `json:"classifiers"`
	} `json:"downloads"`
	Rules   []Rule            `json:"rules"`
	Natives map[string]string `json:"natives"`
	Extract *Extract          `json:"extract"`
}

// Rule 是依赖库的适用规则。
type Rule struct {
	Action string  `json:"action"`
	OS     *OSRule `json:"os"`
}

// OSRule 是规则里的操作系统条件。
type OSRule struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Arch    string `json:"arch"`
}

// Extract 描述原生库解包规则。
type Extract struct {
	Exclude []string `json:"exclude"`
}

// fetchManifest 拉取官方版本清单。
func fetchManifest(ctx context.Context) (*Manifest, error) {
	var m Manifest
	if err := fetchJSON(ctx, manifestURL, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// fetchJSON 拉取并解析 JSON，失败自动重试。
func fetchJSON(ctx context.Context, url string, out any) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", userAgent)
		resp, err := apiClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d %s", resp.StatusCode, url)
			continue
		}
		err = json.NewDecoder(resp.Body).Decode(out)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

// fetchBytes 拉取二进制内容。
func fetchBytes(ctx context.Context, url string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		resp, err := apiClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d %s", resp.StatusCode, url)
			continue
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		return data, nil
	}
	return nil, lastErr
}

// mirrorize 把官方地址前缀替换成自定义镜像前缀。
func mirrorize(u, official, mirror string) string {
	mirror = strings.TrimSpace(mirror)
	if mirror == "" || official == "" {
		return u
	}
	if !strings.HasPrefix(u, official) {
		return u
	}
	rest := strings.TrimPrefix(u, official)
	if !strings.HasSuffix(mirror, "/") {
		mirror += "/"
	}
	return mirror + rest
}

// applyMirrors 按配置替换下载域名（用户在设置里填的自定义镜像）。
func applyMirrors(u string, cfg *Config) string {
	u = mirrorize(u, pistonDataPrefix, cfg.MirrorData)
	u = mirrorize(u, launcherDataPrefix, cfg.MirrorData)
	u = mirrorize(u, resourcePrefix, cfg.MirrorAssets)
	u = mirrorize(u, libraryPrefix, cfg.MirrorLibraries)
	u = mirrorize(u, pistonMetaPrefix, cfg.MirrorMeta)
	u = mirrorize(u, launcherMetaPrefix, cfg.MirrorMeta)
	return u
}

// currentOSName 返回 Mojang 规则里使用的系统名。
func currentOSName() string {
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "osx"
	default:
		return "linux"
	}
}

// currentArch 返回 Mojang 规则里的架构名。
func currentArch() string {
	switch runtime.GOARCH {
	case "arm64":
		return "arm64"
	case "386":
		return "x86"
	case "amd64":
		return "x86_64"
	case "arm":
		return "arm32"
	}
	return runtime.GOARCH
}

// archMatches 判断规则里的架构条件是否匹配本机。
func archMatches(arch string) bool {
	if arch == "" {
		return true
	}
	cur := currentArch()
	if arch == cur {
		return true
	}
	if cur == "x86_64" && (arch == "x64" || arch == "amd64" || arch == "x86-64") {
		return true
	}
	return false
}

// rulesAllow 按 Mojang 规则判断依赖库在当前系统是否适用。
// 语义：没有 rules 视为允许；有 rules 时默认拒绝，只有匹配到的 allow 才放行。
func rulesAllow(rules []Rule) bool {
	if len(rules) == 0 {
		return true
	}
	allowed := false
	for _, r := range rules {
		if r.OS != nil && !osRuleMatches(r.OS) {
			continue
		}
		allowed = r.Action == "allow"
	}
	return allowed
}

var osVersionRe = regexp.MustCompile(`^[\^]?([0-9]+)\.([0-9]+)(\.([0-9]+))?`)

func osRuleMatches(o *OSRule) bool {
	if o.Name != "" && o.Name != currentOSName() {
		return false
	}
	if !archMatches(o.Arch) {
		return false
	}
	if o.Version != "" && o.Name == "osx" {
		m := osVersionRe.FindStringSubmatch(strings.TrimPrefix(o.Version, "^"))
		if m == nil {
			return false
		}
		return true
	}
	return true
}

// skipNativeByArch 按分类名后缀过滤掉不属于本机架构的原生库。
// 现代版本 JSON 会把 natives-windows / -windows-x86 / -windows-arm64 分成三条
// 规则相同的依赖，只能靠名字区分。
func skipNativeByArch(classifier string) bool {
	c := strings.ToLower(classifier)
	if !strings.Contains(c, "natives") {
		return false
	}
	cur := currentArch()
	if strings.HasSuffix(c, "-arm64") && cur != "arm64" {
		return true
	}
	if strings.HasSuffix(c, "-x86") && cur != "x86" {
		return true
	}
	if strings.HasSuffix(c, "-arm32") && cur != "arm32" {
		return true
	}
	return false
}

// libClassifier 取出依赖名的分类部分。
func libClassifier(name string) string {
	if i := strings.LastIndex(name, ":"); i >= 0 {
		return name[i+1:]
	}
	return ""
}

// jsonUnmarshal 解析 JSON 字节。
func jsonUnmarshal(data []byte, out any) error {
	return json.Unmarshal(data, out)
}

// writeFileAtomic 先写临时文件再改名，避免半截文件。
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func dirOf(p string) string {
	i := strings.LastIndexAny(p, `/\`)
	if i < 0 {
		return "."
	}
	return p[:i]
}
