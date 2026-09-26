//go:build windows

// config_test.go —— 持久化设置的往返。
//
// 全部走 load...From/save...From 指到 t.TempDir()：**绝不能碰用户真实的
// %AppData%\clickr** —— 测试机就是用户自己的机器，跑一次测试就把人家的
// 「划词弹出」给关了，那是真事故。

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPopupToggleDefaultsToOn(t *testing.T) {
	// 没有配置文件 = 默认行为 = 开。
	// 这样"第一次运行""用户删掉文件""文件被写坏"是同一件事，不用写分支处理。
	for _, dir := range []string{t.TempDir(), ""} {
		if !loadPopupEnabledFrom(dir) {
			t.Errorf("dir=%q 时默认不是「开」", dir)
		}
	}
}

func TestPopupTogglePersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()

	savePopupEnabledFrom(dir, false)
	if loadPopupEnabledFrom(dir) {
		t.Fatal("关掉之后又读成「开」，重启就会自己开回来")
	}
	if _, err := os.Stat(filepath.Join(dir, popupOffName)); err != nil {
		t.Errorf("关闭状态没有落盘（%s）: %v", popupOffName, err)
	}

	savePopupEnabledFrom(dir, true)
	if !loadPopupEnabledFrom(dir) {
		t.Fatal("重新开启之后仍读成「关」")
	}
	if _, err := os.Stat(filepath.Join(dir, popupOffName)); !os.IsNotExist(err) {
		t.Errorf("重新开启后标记文件还在: %v", err)
	}

	// 重复开启不该报错（文件本来就不存在）
	savePopupEnabledFrom(dir, true)
}

func TestSelectionPopupToggleIsSafeAcrossThreads(t *testing.T) {
	// 开关由 UI 线程写、钩子线程读，所以它不能是一个裸 bool。
	// 这里只做一个基本的可见性冒烟：设完立刻读得到。
	setSelectionPopup(false)
	if selectionPopupEnabled() {
		t.Error("setSelectionPopup(false) 之后仍读到「开」")
	}
	setSelectionPopup(true)
	if !selectionPopupEnabled() {
		t.Error("setSelectionPopup(true) 之后仍读到「关」")
	}
}

func TestSkillsRoundTrip(t *testing.T) {
	dir := t.TempDir()

	// 没有文件 = 全开（返回 nil，由 enabledSkills 兜成"全部技能"）
	if got := loadSkillsFrom(dir); got != nil {
		t.Errorf("没有配置文件时应当返回 nil（= 全开），实际 %v", got)
	}

	saveSkillsFrom(dir, []string{"copy", "translate"})
	got := loadSkillsFrom(dir)
	if len(got) != 2 || got[0] != "copy" || got[1] != "translate" {
		t.Errorf("往返失败：%v", got)
	}

	// 空列表也要能存（此时 enabledSkills 会退回全开，见它自己的注释）
	saveSkillsFrom(dir, nil)
	if got := loadSkillsFrom(dir); len(got) != 0 {
		t.Errorf("存空列表之后读到 %v", got)
	}

	// 目录不存在时不该 panic，只是存不进去
	saveSkillsFrom("", []string{"copy"})
}
