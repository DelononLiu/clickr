//go:build windows

// menucontent.go —— 菜单里**有哪些动作**、各自的 URL。
//
// 这一块是业务政策，不是界面：它跟"怎么逐像素画一个圆角矩形"没有任何共同的变化原因。

package main

import (
	"log"
	"net/url"
	"unsafe"
)

const (
	iconCopy = iota + 1
	iconSearch
	iconTranslate
	iconClose
)

type menuItem struct {
	title    string
	shortcut string
	icon     int
	run      func()
}

type menuModel struct {
	items []menuItem
}

func selectionMenu(text string) menuModel {
	q := url.QueryEscape(text)
	return menuModel{items: []menuItem{
		{title: "复制", shortcut: "Ctrl+C", icon: iconCopy, run: func() {
			if err := setClipboardText(text); err != nil {
				log.Printf("[action] 复制失败: %v", err)
			}
		}},
		{title: "搜索", shortcut: "Enter", icon: iconSearch, run: func() {
			shellOpen("https://www.bing.com/search?q=" + q)
		}},
		{title: "翻译", shortcut: "Ctrl+T", icon: iconTranslate, run: func() {
			shellOpen("https://dict.youdao.com/result?word=" + q + "&lang=en")
		}},
	}}
}

func shellOpen(url string) {
	op := utf16Ptr("open")
	pShellExecuteW.Call(0, uintptr(unsafe.Pointer(op)),
		uintptr(unsafe.Pointer(utf16Ptr(url))), 0, 0, swShownormal)
}
