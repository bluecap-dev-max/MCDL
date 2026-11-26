//go:build windows

package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procGetStdHandle        = kernel32.NewProc("GetStdHandle")
	procGetConsoleMode      = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode      = kernel32.NewProc("SetConsoleMode")
	procGetConsoleScreenBuf = kernel32.NewProc("GetConsoleScreenBufferInfo")
	procReadConsoleInput    = kernel32.NewProc("ReadConsoleInputW")
	procPeekConsoleInput    = kernel32.NewProc("PeekConsoleInputW")
	procSetConsoleCP        = kernel32.NewProc("SetConsoleCP")
	procSetConsoleOutputCP  = kernel32.NewProc("SetConsoleOutputCP")
	procSetConsoleCtrl      = kernel32.NewProc("SetConsoleCtrlHandler")
)

const (
	enableLineInput    = 0x0002
	enableEchoInput    = 0x0004
	enableInsertMode   = 0x0020
	enableQuickEdit    = 0x0040
	enableWindowInput  = 0x0100
	enableVirtualTerm  = 0x0004
	enableVirtualInput = 0x0200
)

// Windows 控制台标准句柄（-10 / -11，需用无符号写法）。
const (
	stdInputHandle  = ^uintptr(0) - 9
	stdOutputHandle = ^uintptr(0) - 10
)

// 虚拟键码。
const (
	vkBack   = 0x08
	vkTab    = 0x09
	vkReturn = 0x0d
	vkEscape = 0x1b
	vkPrior  = 0x21
	vkNext   = 0x22
	vkEnd    = 0x23
	vkHome   = 0x24
	vkLeft   = 0x25
	vkUp     = 0x26
	vkRight  = 0x27
	vkDown   = 0x28
	vkDelete = 0x2e
)

type coord struct {
	X int16
	Y int16
}

type smallRect struct {
	Left   int16
	Top    int16
	Right  int16
	Bottom int16
}

type consoleScreenBufferInfo struct {
	Size              coord
	CursorPosition    coord
	Attributes        uint16
	Window            smallRect
	MaximumWindowSize coord
}

type keyEventRecord struct {
	BoolKeyDown     int32
	RepeatCount     uint16
	VirtualKeyCode  uint16
	VirtualScanCode uint16
	UnicodeChar     uint16
	ControlKeyState uint32
}

type inputRecord struct {
	EventType uint16
	_         uint16
	KeyEvent  keyEventRecord
}

// globalRestore 在控制事件里恢复控制台设置。
var globalRestore func()

func consoleCtrlHandler(ctrl uintptr) uintptr {
	if globalRestore != nil {
		globalRestore()
	}
	os.Exit(130)
	return 1
}

// Console 封装 Windows 控制台。
type Console struct {
	*keyReader
	in  syscall.Handle
	out syscall.Handle
	// consoleEvents 为真表示输入来自真实控制台，走 ReadConsoleInputW；
	// 为假表示 stdin 是管道/文件，走字节流解析。
	consoleEvents bool
	origIn        uint32
	origOut       uint32
	rawOn         bool
	pend          []inputRecord
}

// NewConsole 打开控制台并进入原始模式。
func NewConsole() (*Console, error) {
	hIn, _, _ := procGetStdHandle.Call(stdInputHandle)
	hOut, _, _ := procGetStdHandle.Call(stdOutputHandle)
	bad := ^uintptr(0)
	if hIn == 0 || hIn == bad || hOut == 0 || hOut == bad {
		return nil, errors.New("当前不是交互式控制台，请使用 --cli 模式")
	}
	c := &Console{
		in:  syscall.Handle(hIn),
		out: syscall.Handle(hOut),
	}

	// 让控制台按 UTF-8 输出，中文界面才不会乱码
	procSetConsoleOutputCP.Call(uintptr(65001))
	procSetConsoleCP.Call(uintptr(65001))

	var mode uint32
	if r, _, _ := procGetConsoleMode.Call(uintptr(c.in), uintptr(unsafe.Pointer(&mode))); r != 0 {
		c.origIn = mode
		c.consoleEvents = true
		// 关掉行编辑/回显，开启 VT 输入与窗口输入
		newMode := mode &^ (enableLineInput | enableEchoInput | enableQuickEdit | enableInsertMode)
		newMode |= enableWindowInput | enableVirtualInput
		procSetConsoleMode.Call(uintptr(c.in), uintptr(newMode))
		c.rawOn = true
	}
	// 控制台只能有一个读取者：真控制台用 ReadConsoleInputW，管道/文件才用字节流
	if c.consoleEvents {
		c.keyReader = newKeyReader(nil)
	} else {
		c.keyReader = newKeyReader(os.Stdin)
	}
	var omode uint32
	if r, _, _ := procGetConsoleMode.Call(uintptr(c.out), uintptr(unsafe.Pointer(&omode))); r != 0 {
		c.origOut = omode
		procSetConsoleMode.Call(uintptr(c.out), uintptr(omode|enableVirtualTerm))
	}

	globalRestore = c.restore
	procSetConsoleCtrl.Call(syscall.NewCallback(consoleCtrlHandler), 1)
	return c, nil
}

// restore 还原控制台原始设置。
func (c *Console) restore() {
	if c.rawOn {
		procSetConsoleMode.Call(uintptr(c.in), uintptr(c.origIn))
		c.rawOn = false
	}
	if c.origOut != 0 {
		procSetConsoleMode.Call(uintptr(c.out), uintptr(c.origOut))
		c.origOut = 0
	}
}

// Close 恢复控制台设置。
func (c *Console) Close() { c.restore() }

// IsTerminal 返回当前是否连着真实控制台。
func (c *Console) IsTerminal() bool {
	var mode uint32
	r, _, _ := procGetConsoleMode.Call(uintptr(c.out), uintptr(unsafe.Pointer(&mode)))
	return r != 0
}

// Size 返回终端列数与行数。
func (c *Console) Size() (int, int) {
	var info consoleScreenBufferInfo
	r, _, _ := procGetConsoleScreenBuf.Call(uintptr(c.out), uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 80, 24
	}
	w := int(info.Window.Right - info.Window.Left + 1)
	if w <= 0 {
		w = int(info.Size.X)
	}
	h := int(info.Window.Bottom - info.Window.Top + 1)
	if h <= 0 {
		h = int(info.Size.Y)
	}
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	return w, h
}

// ReadKey 读取一个按键。
func (c *Console) ReadKey() (Key, error) {
	if c.consoleEvents {
		return c.readKeyEvent()
	}
	return c.readKey()
}

// rawRead 从控制台读一批输入记录。
func (c *Console) rawRead() ([]inputRecord, error) {
	var recs [64]inputRecord
	var n uint32
	r, _, err := procReadConsoleInput.Call(uintptr(c.in),
		uintptr(unsafe.Pointer(&recs[0])), uintptr(len(recs)), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		return nil, err
	}
	out := make([]inputRecord, 0, n)
	for i := uint32(0); i < n; i++ {
		out = append(out, recs[i])
	}
	return out, nil
}

// pop 取一条记录：优先用本地队列，空则从控制台读。
func (c *Console) pop() (inputRecord, bool) {
	if len(c.pend) == 0 {
		recs, err := c.rawRead()
		if err != nil {
			return inputRecord{}, false
		}
		c.pend = append(c.pend, recs...)
	}
	if len(c.pend) == 0 {
		return inputRecord{}, false
	}
	rec := c.pend[0]
	c.pend = c.pend[1:]
	return rec, true
}

// popTimeout 带超时地取一条记录（先看队列，再看控制台是否有输入）。
func (c *Console) popTimeout(d time.Duration) (inputRecord, bool) {
	if len(c.pend) > 0 {
		rec := c.pend[0]
		c.pend = c.pend[1:]
		return rec, true
	}
	var recs [8]inputRecord
	var n uint32
	r, _, _ := procPeekConsoleInput.Call(uintptr(c.in),
		uintptr(unsafe.Pointer(&recs[0])), uintptr(len(recs)), uintptr(unsafe.Pointer(&n)))
	if r == 0 || n == 0 {
		time.Sleep(d)
		return inputRecord{}, false
	}
	got, err := c.rawRead()
	if err != nil || len(got) == 0 {
		time.Sleep(d)
		return inputRecord{}, false
	}
	c.pend = append(c.pend, got...)
	rec := c.pend[0]
	c.pend = c.pend[1:]
	return rec, true
}

// readKeyEvent 从控制台按键事件里解析一个按键。
func (c *Console) readKeyEvent() (Key, error) {
	for {
		rec, ok := c.pop()
		if !ok {
			return Key{}, io.EOF
		}
		if rec.EventType != 1 { // 只要 KEY_EVENT
			continue
		}
		ke := rec.KeyEvent
		if ke.BoolKeyDown == 0 {
			continue
		}
		k := Key{
			Ctrl:  ke.ControlKeyState&0x02 != 0,
			Alt:   ke.ControlKeyState&0x01 != 0,
			Shift: ke.ControlKeyState&0x10 != 0,
		}
		switch ke.VirtualKeyCode {
		case vkUp:
			k.Kind = KeyUp
			return k, nil
		case vkDown:
			k.Kind = KeyDown
			return k, nil
		case vkLeft:
			k.Kind = KeyLeft
			return k, nil
		case vkRight:
			k.Kind = KeyRight
			return k, nil
		case vkHome:
			k.Kind = KeyHome
			return k, nil
		case vkEnd:
			k.Kind = KeyEnd
			return k, nil
		case vkPrior:
			k.Kind = KeyPgUp
			return k, nil
		case vkNext:
			k.Kind = KeyPgDn
			return k, nil
		case vkDelete:
			k.Kind = KeyDelete
			return k, nil
		case vkReturn:
			k.Kind = KeyEnter
			return k, nil
		case vkEscape:
			if seq, ok := c.assembleSeq(); ok {
				return seq, nil
			}
			k.Kind = KeyEsc
			return k, nil
		case vkBack:
			k.Kind = KeyBackspace
			return k, nil
		case vkTab:
			k.Kind = KeyTab
			return k, nil
		}
		ch := rune(ke.UnicodeChar)
		if ch == 0 {
			continue
		}
		// 某些终端（VT 输入模式）会把控制键当作字符下发，这里统一归一化
		switch ch {
		case '\r', '\n':
			k.Kind = KeyEnter
			return k, nil
		case 0x08, 0x7f:
			k.Kind = KeyBackspace
			return k, nil
		case '\t':
			k.Kind = KeyTab
			return k, nil
		case 3:
			k.Kind = KeyCtrlC
			return k, nil
		case 0x1b:
			if seq, ok := c.assembleSeq(); ok {
				return seq, nil
			}
			k.Kind = KeyEsc
			return k, nil
		}
		k.Kind = KeyRune
		k.R = ch
		return k, nil
	}
}

// assembleSeq 把 ESC 后面的字符拼成一个方向键/功能键。
// 关键点：后续字符可能已经在本次读出的批次里，必须先从本地队列取，再去控制台等。
func (c *Console) assembleSeq() (Key, bool) {
	var chars []rune
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		rec, ok := c.popTimeout(8 * time.Millisecond)
		if !ok {
			continue
		}
		if rec.EventType != 1 || rec.KeyEvent.BoolKeyDown == 0 {
			continue
		}
		ch := rune(rec.KeyEvent.UnicodeChar)
		if ch == 0 {
			return Key{}, false
		}
		if len(chars) == 0 {
			if ch != '[' && ch != 'O' {
				return Key{}, false
			}
			chars = append(chars, ch)
			continue
		}
		chars = append(chars, ch)
		if ch == '~' || (ch >= 'A' && ch <= 'Z') {
			return mapSeq(string(chars))
		}
		if len(chars) > 8 {
			return Key{}, false
		}
	}
	return Key{}, false
}

// mapSeq 把 "[A"、"[6~"、"OQ" 之类的序列映射成按键。
func mapSeq(s string) (Key, bool) {
	// 去掉修饰键参数（"[1;5A" -> "[A"）
	if i := indexByteStr(s, ';'); i > 0 && i < len(s)-1 {
		s = s[:i] + s[len(s)-1:]
	}
	switch s {
	case "[A", "OA":
		return Key{Kind: KeyUp}, true
	case "[B", "OB":
		return Key{Kind: KeyDown}, true
	case "[C", "OC":
		return Key{Kind: KeyRight}, true
	case "[D", "OD":
		return Key{Kind: KeyLeft}, true
	case "[H", "OH", "[1~", "[7~":
		return Key{Kind: KeyHome}, true
	case "[F", "OF", "[4~", "[8~":
		return Key{Kind: KeyEnd}, true
	case "[5~":
		return Key{Kind: KeyPgUp}, true
	case "[6~":
		return Key{Kind: KeyPgDn}, true
	case "[3~":
		return Key{Kind: KeyDelete}, true
	case "[Z":
		return Key{Kind: KeyTab, Shift: true}, true
	}
	return Key{}, false
}

// indexByteStr 返回字节 codepoint 在字符串里的位置（仅用于 ASCII）。
func indexByteStr(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// Write 输出文本。
func (c *Console) Write(s string) {
	os.Stdout.WriteString(s)
}

// OpenFolder 用资源管理器打开目录。
func OpenFolder(path string) error {
	return exec.Command("explorer", path).Start()
}
