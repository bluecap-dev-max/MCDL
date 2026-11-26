package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config 是持久化配置，存在 %APPDATA%\mcdl\config.json。
type Config struct {
	OutputDir       string `json:"outputDir"`
	Launcher        string `json:"launcher"`
	Layout          string `json:"layout"`
	Source          string `json:"source"`
	Concurrency     int    `json:"concurrency"`
	DownloadClient  bool   `json:"downloadClient"`
	DownloadServer  bool   `json:"downloadServer"`
	DownloadMapping bool   `json:"downloadMapping"`
	DownloadAssets  bool   `json:"downloadAssets"`
	DownloadLibs    bool   `json:"downloadLibs"`
	Resume          bool   `json:"resume"`
	VerifySHA1      bool   `json:"verifySha1"`
	Retry           int    `json:"retry"`
	SkipExisting    bool   `json:"skipExisting"`
	ExtractNatives  bool   `json:"extractNatives"`
	LauncherProfile bool   `json:"launcherProfiles"`
	ExportZip       string `json:"exportZip"`
	MirrorData      string `json:"mirrorData"`
	MirrorAssets    string `json:"mirrorAssets"`
	MirrorLibraries string `json:"mirrorLibraries"`
	MirrorMeta      string `json:"mirrorMeta"`
	ASCII           bool   `json:"ascii"`
}

// DefaultConfig 返回默认配置。
func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	out := filepath.Join(home, ".minecraft")
	if v := os.Getenv("MCDL_OUT"); v != "" {
		out = v
	}
	return &Config{
		OutputDir:       out,
		Launcher:        "",
		Layout:          layoutStandard,
		Source:          sourceAuto,
		Concurrency:     8,
		DownloadClient:  true,
		DownloadServer:  true,
		DownloadMapping: false,
		DownloadAssets:  false,
		DownloadLibs:    true,
		Resume:          true,
		VerifySHA1:      true,
		Retry:           3,
		SkipExisting:    true,
		ExtractNatives:  false,
		LauncherProfile: true,
		ExportZip:       "",
		MirrorData:      "",
		MirrorAssets:    "",
		MirrorLibraries: "",
		MirrorMeta:      "",
		ASCII:           false,
	}
}

func configDir() string {
	if v := os.Getenv("MCDL_CONFIG_DIR"); v != "" {
		return v
	}
	base := os.Getenv("APPDATA")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, "AppData", "Roaming")
	}
	return filepath.Join(base, "mcdl")
}

func configPath() string { return filepath.Join(configDir(), "config.json") }

// LoadConfig 读取配置；文件不存在时返回默认配置。
func LoadConfig() *Config {
	cfg := DefaultConfig()
	data, err := os.ReadFile(configPath())
	if err == nil {
		// 记事本等编辑器可能写入 UTF-8 BOM，先去掉
		data = bytesTrimBOM(data)
		_ = json.Unmarshal(data, cfg)
	}
	cfg.normalize()
	return cfg
}

// bytesTrimBOM 去掉 UTF-8 BOM。
func bytesTrimBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}

// Save 写回配置。
func (c *Config) Save() error {
	if err := os.MkdirAll(configDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), data, 0o644)
}

func (c *Config) normalize() {
	if c.Concurrency < 1 {
		c.Concurrency = 1
	}
	if c.Concurrency > 32 {
		c.Concurrency = 32
	}
	if c.Retry < 0 {
		c.Retry = 0
	}
	if c.Retry > 10 {
		c.Retry = 10
	}
	if strings.TrimSpace(c.OutputDir) == "" {
		c.OutputDir = DefaultConfig().OutputDir
	}
	if !isLayoutName(c.Layout) {
		c.Layout = layoutStandard
	}
	if !isSourceName(c.Source) {
		c.Source = sourceAuto
	}
}

// humanSize 把字节数格式化成易读形式。
func humanSize(n int64) string {
	const unit = 1024
	if n < 0 {
		return "-"
	}
	f := float64(n)
	units := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	for f >= unit && i < len(units)-1 {
		f /= unit
		i++
	}
	if i == 0 {
		return strconv.FormatInt(n, 10) + " " + units[i]
	}
	return strconv.FormatFloat(f, 'f', 1, 64) + " " + units[i]
}

// humanDuration 把秒数格式化成 HH:MM:SS。
func humanDuration(sec float64) string {
	if sec < 0 || sec > 1e9 {
		return "--:--"
	}
	total := int(sec + 0.5)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}
