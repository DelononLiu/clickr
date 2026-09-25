//go:build windows

// input.go —— 合成输入。
//
// 单独成文件是因为它是本项目**唯一会主动向外发按键**的地方 ——
// 「会不会打断用户的命令」这类判断都围着它转，值得一眼能找到。

package main

import "unsafe"

// isKeyDown 查询按键当前是否按下。
func isKeyDown(vk uintptr) bool {
	v, _, _ := pGetAsyncKeyState.Call(vk)
	return int16(uint16(v)) < 0
}

func sendKey(vk uint16, up bool) {
	flags := uint32(0)
	if up {
		flags = keyeventfKeyUp
	}
	in := input{Type: 1 /*INPUT_KEYBOARD*/, Ki: keybdInput{WVk: vk, DwFlags: flags}}
	pSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
}

// sendCtrlC 模拟一次 Ctrl+C。
//
// 关键点：用户在划词时很可能正按着 Shift（或者别的方式），
// 这里额外补发一次 Ctrl 抬起，避免修饰键状态被我们搞乱。
func sendCtrlC() {
	// 关键：只有**我们自己按下**的 Ctrl 才由我们抬起。
	//
	// 用户很可能正按着 Ctrl 在别处操作；无条件补一次「抬起」会把他的 Ctrl
	// 松开（修饰键状态错乱）。之前的条件只挡住了「按下」，没挡住「抬起」。
	wePressedCtrl := false
	if !isKeyDown(vkControl) {
		sendKey(vkControl, false)
		wePressedCtrl = true
	}
	sendKey(vkC, false)
	sendKey(vkC, true)
	if wePressedCtrl {
		sendKey(vkControl, true)
	}
}
