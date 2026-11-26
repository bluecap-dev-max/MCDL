//go:build !windows

package main

import (
	"os"
	"os/exec"
)

// Console 是非 Windows 平台的简化实现（同样使用字节流读键）。
type Console struct {
	*keyReader
}

// NewConsole 返回一个简化控制台。
func NewConsole() (*Console, error) {
	return &Console{keyReader: newKeyReader(os.Stdin)}, nil
}

// Close 无操作。
func (c *Console) Close() {}

// IsTerminal 判断是否连着终端。
func (c *Console) IsTerminal() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// Size 返回默认尺寸。
func (c *Console) Size() (int, int) { return 100, 30 }

// ReadKey 读取一个按键。
func (c *Console) ReadKey() (Key, error) { return c.readKey() }

// Write 输出文本。
func (c *Console) Write(s string) { os.Stdout.WriteString(s) }

// OpenFolder 用系统文件管理器打开目录。
func OpenFolder(path string) error {
	return exec.Command("xdg-open", path).Start()
}
