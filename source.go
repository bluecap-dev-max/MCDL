package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// 下载源 ID。
const (
	sourceAuto     = "auto"
	sourceOfficial = "official"
	sourceBMCL     = "bmclapi"
	sourceBMCLAlt  = "bmclapi-alt"
	sourceCustom   = "custom"
)

// sourceDef 描述一个下载源。
type sourceDef struct {
	ID   string
	Name string
	Base string // 镜像基址（BMCLAPI 风格）；空表示官方
}

// sourceDefs 是全部可选源（官方在前）。
var sourceDefs = []sourceDef{
	{ID: sourceOfficial, Name: "官方源", Base: ""},
	{ID: sourceBMCL, Name: "BMCLAPI", Base: "https://bmclapi2.bangbang93.com"},
	{ID: sourceBMCLAlt, Name: "BMCLAPI 备用", Base: "https://bmclapi.bangbang93.com"},
}

// sourceIDs 是选择器里展示的顺序（自动 + 自定义 在两端）。
var sourceIDs = []string{sourceAuto, sourceOfficial, sourceBMCL, sourceBMCLAlt, sourceCustom}

// sourceByID 查找源定义。
func sourceByID(id string) (sourceDef, bool) {
	for _, d := range sourceDefs {
		if d.ID == id {
			return d, true
		}
	}
	return sourceDef{}, false
}

// isSourceName 判断是否是合法的下载源名。
func isSourceName(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, n := range sourceIDs {
		if s == n {
			return true
		}
	}
	return false
}

// sourceLabel 返回下载源的中文名。
func sourceLabel(s string) string {
	switch s {
	case sourceBMCL:
		return "BMCLAPI"
	case sourceBMCLAlt:
		return "BMCLAPI 备用"
	case sourceOfficial:
		return "官方源"
	case sourceCustom:
		return "自定义镜像"
	default:
		return "自动（测速）"
	}
}

// nextSource 在下载源之间循环切换。
func nextSource(cur string, delta int) string {
	idx := 0
	for i, n := range sourceIDs {
		if n == cur {
			idx = i
			break
		}
	}
	idx = (idx + delta + len(sourceIDs)) % len(sourceIDs)
	return sourceIDs[idx]
}

// mirrorURL 按镜像基址重写地址。base 为空表示官方（返回 false）。
func mirrorURL(base, kind, versionID, officialURL, mavenPath, hash string) (string, bool) {
	if base == "" {
		return "", false
	}
	switch kind {
	case "json":
		return base + "/version/" + versionID + "/json", true
	case "client":
		return base + "/version/" + versionID + "/client", true
	case "server":
		return base + "/version/" + versionID + "/server", true
	case "library", "native":
		// 只镜像 libraries.minecraft.net 上的库，Maven Central 的不能套
		if mavenPath == "" || !strings.HasPrefix(officialURL, libraryPrefix) {
			return "", false
		}
		return base + "/maven/" + strings.TrimPrefix(mavenPath, "/"), true
	case "asset":
		if len(hash) < 2 {
			return "", false
		}
		return base + "/assets/" + hash[:2] + "/" + hash, true
	}
	return "", false
}

// probeSpeed 实测下载 256KB 的速度（KB/s），失败返回 -1。
func probeSpeed(url string) int64 {
	client := &http.Client{Timeout: 10 * time.Second}
	const probeBytes = 262144
	best := int64(-1)
	for i := 0; i < 2; i++ {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return -1
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", probeBytes-1))
		start := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		n, _ := io.Copy(io.Discard, io.LimitReader(resp.Body, probeBytes))
		resp.Body.Close()
		elapsed := time.Since(start).Seconds()
		if resp.StatusCode >= 400 || n == 0 || elapsed <= 0 {
			continue
		}
		kbs := int64(float64(n) / 1024 / elapsed)
		if best < 0 || kbs > best {
			best = kbs
		}
	}
	return best
}

// probeBestSource 并发测速所有源，返回最快的那个及各自的测速结果。
func probeBestSource() (string, map[string]int64) {
	const probePath = "org/lwjgl/lwjgl/3.3.3/lwjgl-3.3.3.jar"
	results := map[string]int64{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, d := range sourceDefs {
		url := "https://libraries.minecraft.net/" + probePath
		if d.Base != "" {
			url = d.Base + "/maven/" + probePath
		}
		wg.Add(1)
		go func(id, u string) {
			defer wg.Done()
			kbs := probeSpeed(u)
			mu.Lock()
			results[id] = kbs
			mu.Unlock()
		}(d.ID, url)
	}
	wg.Wait()

	best := sourceOfficial
	bestSpeed := int64(-1)
	for _, d := range sourceDefs {
		kbs := results[d.ID]
		if kbs >= 0 && kbs > bestSpeed {
			best, bestSpeed = d.ID, kbs
		}
	}
	return best, results
}

// describeProbe 生成测速结果说明。
func describeProbe(results map[string]int64) string {
	parts := make([]string, 0, len(sourceDefs))
	for _, d := range sourceDefs {
		kbs := results[d.ID]
		if kbs < 0 {
			parts = append(parts, d.Name+" 超时")
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %d KB/s", d.Name, kbs))
	}
	return strings.Join(parts, " / ")
}
