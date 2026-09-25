//go:build windows

// clipboard_test.go —— 剪贴板快照与格式分类。
//
// 交叉编译后直接跑：
//
//	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o clickr.test.exe .
//	./clickr.test.exe -test.v

package main

import (
	"testing"
	"time"
)

// 这是针对「划词弄丢用户剪贴板」的回归测试，直接复现失效场景：
// 快照 → 被覆盖（相当于我们发的 Ctrl+C）→ 还原。
//
// 之前用 OleGetClipboard/OleSetClipboard 的版本在这里必然失败：
// 代理对象在剪贴板被改动后就失效了。
func TestClipboardSnapshotSurvivesOverwrite(t *testing.T) {
	// 先保住用户真实的剪贴板，测完放回去
	userSnap := snapshotClipboard()
	defer userSnap.restore()

	const marker = "clickr 快照往返测试 A1B2C3"
	if err := setClipboardText(marker); err != nil {
		t.Skipf("写剪贴板失败（可能有别的程序占着）: %v", err)
	}
	time.Sleep(80 * time.Millisecond)

	snap := snapshotClipboard()
	if len(snap.formats) == 0 {
		t.Fatal("快照为空，说明 EnumClipboardFormats/GetClipboardData 有问题")
	}
	var hasText bool
	for _, f := range snap.formats {
		if f.format == cfUnicodeText {
			hasText = true
		}
	}
	if !hasText {
		t.Error("快照里应当包含 CF_UNICODETEXT")
	}

	// 模拟被我们发出的 Ctrl+C 覆盖掉
	if err := setClipboardText("覆盖掉的内容-XXXX"); err != nil {
		t.Fatalf("覆盖剪贴板失败: %v", err)
	}
	// 剪贴板是全局资源，写入到可读之间偶发观察不到（实测过）。
	// 加一个短重试，并在彻底失败时把现场打出来，别只报一个空字符串。
	var got string
	for i := 0; i < 10; i++ {
		if got, _ = readClipboardText(); got == "覆盖掉的内容-XXXX" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got != "覆盖掉的内容-XXXX" {
		avail, _, _ := pIsClipboardFormatAvailable.Call(cfUnicodeText)
		opened, _, oerr := pOpenClipboard.Call(0)
		if opened != 0 {
			pCloseClipboard.Call()
		}
		t.Fatalf("预期剪贴板已被覆盖，实际 %q（seq=%d 文本格式可用=%d OpenClipboard=%d err=%v）",
			got, clipboardSequence(), avail, opened, oerr)
	}

	// 还原
	if !snap.restore() {
		t.Fatal("快照还原失败")
	}
	got, ok := readClipboardText()
	if !ok {
		t.Fatal("还原后读不到文本")
	}
	if got != marker {
		t.Errorf("还原后内容 = %q，期望 %q", got, marker)
	}
	// 快照里的每个格式都应当回到剪贴板上
	for _, f := range snap.formats {
		if r, _, _ := pIsClipboardFormatAvailable.Call(uintptr(f.format)); r == 0 {
			t.Errorf("格式 %d 还原后不存在", f.format)
		}
	}
}

// 逐个格式快照必须能正确区分 HGLOBAL 和 GDI 句柄：
// 把 HBITMAP 当 HGLOBAL 去 GlobalLock 会拿到垃圾数据甚至崩。
func TestGlobalHandleFormatClassification(t *testing.T) {
	cases := map[uint32]bool{
		cfUnicodeText:  true,  // HGLOBAL
		1:              true,  // CF_TEXT
		8:              true,  // CF_DIB
		15:             true,  // CF_HDROP（复制的文件）
		0xC000:         true,  // 注册格式，一律 HGLOBAL
		0xC123:         true,  // HTML Format / RTF 之类
		cfBitmap:       false, // HBITMAP，GDI 对象
		cfPalette:      false, // HPALETTE，GDI 对象
		cfEnhMetafile:  false, // HENHMETAFILE，GDI 对象
		cfOwnerDisplay: false, // 由所有者自绘，句柄为 NULL
		cfDspBitmap:    false, // HBITMAP

		// CF_METAFILEPICT / CF_DSPMETAFILEPICT 常被误当成 GDI 句柄，
		// 实际传的是「装着 METAFILEPICT 结构的全局内存句柄」，是 HGLOBAL，
		// 可以按字节直接拷。别和 HENHMETAFILE 混。
		cfMetafilePict:    true,
		cfDspMetafilePict: true,
		0x0200:            false, // 私有句柄区
		0x0300:            false, // GDI 对象区
	}
	for f, want := range cases {
		if got := isGlobalHandleFormat(f); got != want {
			t.Errorf("isGlobalHandleFormat(%#x) = %v，期望 %v", f, got, want)
		}
	}
}

func TestSetAndReadClipboardRoundTrip(t *testing.T) {
	userSnap := snapshotClipboard()
	defer userSnap.restore()

	const want = "往返测试-中文与符号 !@#$%"
	if err := setClipboardText(want); err != nil {
		t.Skipf("写剪贴板失败: %v", err)
	}
	time.Sleep(60 * time.Millisecond)
	got, ok := readClipboardText()
	if !ok || got != want {
		t.Errorf("读回 %q (ok=%v)，期望 %q", got, ok, want)
	}
	if setClipboardText("") == nil {
		t.Error("空文本应当报错")
	}
}
