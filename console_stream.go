package main

import (
	"io"
	"time"
)

// keyReader 从字节流中解析按键。
// 只用于 stdin 是管道/文件的情况；输入是真实控制台时走 ReadConsoleInputW。
type keyReader struct {
	ch      chan byte
	pending []byte
}

// newKeyReader 创建读取器。r 为 nil 时不启动后台协程。
func newKeyReader(r io.Reader) *keyReader {
	kr := &keyReader{ch: make(chan byte, 8192)}
	if r != nil {
		go kr.loop(r)
	}
	return kr
}

// loop 持续把字节读进通道。
func (k *keyReader) loop(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		for i := 0; i < n; i++ {
			k.ch <- buf[i]
		}
		if err != nil {
			close(k.ch)
			return
		}
	}
}

// next 读取下一个字节，timeout<=0 表示阻塞等待。
func (k *keyReader) next(timeout time.Duration) (byte, bool) {
	if len(k.pending) > 0 {
		b := k.pending[0]
		k.pending = k.pending[1:]
		return b, true
	}
	if timeout <= 0 {
		b, ok := <-k.ch
		return b, ok
	}
	select {
	case b, ok := <-k.ch:
		return b, ok
	case <-time.After(timeout):
		return 0, false
	}
}

// push 把字节放回队列。
func (k *keyReader) push(b byte) {
	k.pending = append([]byte{b}, k.pending...)
}

// readKey 解析出一个按键。
func (k *keyReader) readKey() (Key, error) {
	b, ok := k.next(0)
	if !ok {
		return Key{}, io.EOF
	}
	switch b {
	case 0x1b:
		b2, ok := k.next(150 * time.Millisecond)
		if !ok {
			return Key{Kind: KeyEsc}, nil
		}
		if b2 == '[' || b2 == 'O' {
			return k.readCSI(b2)
		}
		k.push(b2)
		return Key{Kind: KeyEsc}, nil
	case '\r', '\n':
		return Key{Kind: KeyEnter}, nil
	case 0x08, 0x7f:
		return Key{Kind: KeyBackspace}, nil
	case '\t':
		return Key{Kind: KeyTab}, nil
	case 0x03:
		return Key{Kind: KeyCtrlC}, nil
	case 0x00:
		return Key{}, nil
	}
	if b < 0x80 {
		return Key{Kind: KeyRune, R: rune(b)}, nil
	}
	// UTF-8 多字节字符
	buf := []byte{b}
	for i := 0; i < 3; i++ {
		nb, ok := k.next(300 * time.Millisecond)
		if !ok {
			break
		}
		buf = append(buf, nb)
		if nb&0xC0 != 0x80 {
			break
		}
	}
	rs := []rune(string(buf))
	if len(rs) == 0 {
		return Key{}, nil
	}
	return Key{Kind: KeyRune, R: rs[0]}, nil
}

// readCSI 解析 ESC [ ... 序列。
func (k *keyReader) readCSI(prefix byte) (Key, error) {
	var params []byte
	for {
		b, ok := k.next(400 * time.Millisecond)
		if !ok {
			return Key{}, nil
		}
		if b >= 0x40 && b <= 0x7e {
			ctrl := false
			shift := false
			alt := false
			p := string(params)
			for _, part := range splitParams(p) {
				switch part {
				case "5":
					ctrl = true
				case "2":
					shift = true
				case "3":
					alt = true
				}
			}
			mk := func(kind int) Key {
				return Key{Kind: kind, Ctrl: ctrl, Shift: shift, Alt: alt}
			}
			switch b {
			case 'A':
				return mk(KeyUp), nil
			case 'B':
				return mk(KeyDown), nil
			case 'C':
				return mk(KeyRight), nil
			case 'D':
				return mk(KeyLeft), nil
			case 'H':
				return mk(KeyHome), nil
			case 'F':
				return mk(KeyEnd), nil
			case 'Z':
				return Key{Kind: KeyTab, Shift: true}, nil
			}
			switch p {
			case "1", "7":
				return mk(KeyHome), nil
			case "4", "8":
				return mk(KeyEnd), nil
			case "5":
				return mk(KeyPgUp), nil
			case "6":
				return mk(KeyPgDn), nil
			case "3":
				return mk(KeyDelete), nil
			}
			return Key{}, nil
		}
		params = append(params, b)
	}
}

// splitParams 按 ';' 拆分参数。
func splitParams(s string) []string {
	var out []string
	cur := ""
	for i := 0; i < len(s); i++ {
		if s[i] == ';' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(s[i])
	}
	out = append(out, cur)
	return out
}
