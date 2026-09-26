//go:build windows

// config.go —— 持久化设置（都在 %AppData%\clickr\ 下）。
//
// 目前有三件：悬浮球位置、划词自动弹菜单的开关、开启的技能列表。
// 放在一个文件里是因为它们的变化原因是同一件事：**用户改了一项设置，希望重启后还在**。
//
// 读设置失败一律退回默认值并且**只记日志、不拦启动** —— 配置坏了不该让程序起不来。

package main

import (
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// configDir 返回 %AppData%\clickr；拿不到就返回空串（调用方跳过读写）。
func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "clickr")
}

// ================================================================ 悬浮球位置

func posFile() string {
	if d := configDir(); d != "" {
		return filepath.Join(d, "ball.pos")
	}
	return ""
}

func loadBallPos() (point, bool) {
	f := posFile()
	if f == "" {
		return point{}, false
	}
	b, err := os.ReadFile(f)
	if err != nil {
		return point{}, false
	}
	parts := strings.Fields(strings.TrimSpace(string(b)))
	if len(parts) != 2 {
		return point{}, false
	}
	x, err1 := strconv.Atoi(parts[0])
	y, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return point{}, false
	}
	return point{int32(x), int32(y)}, true
}

func saveBallPos(pt point) {
	f := posFile()
	if f == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		log.Printf("[config] 保存位置失败: %v", err)
		return
	}
	data := strconv.Itoa(int(pt.X)) + " " + strconv.Itoa(int(pt.Y))
	if err := os.WriteFile(f, []byte(data), 0o644); err != nil {
		log.Printf("[config] 保存位置失败: %v", err)
	}
}

// ================================================================ 「划词自动弹菜单」开关

// 用**文件存不存在**表示关闭，而不是往文件里写 on/off：
// 默认值是"开"，于是「没有文件」天然就是默认行为 ——
// 程序第一次跑、用户删掉这个文件、文件被写坏，全都是同一件事：开着。
// 不用再处理"文件内容不合法"这种分支。
const popupOffName = "popup.off"

func popupOffFile(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, popupOffName)
}

// loadPopupEnabled / savePopupEnabled 是给程序用的；带 ...From 的版本让测试
// 指到临时目录，免得测试去改用户真实的设置。
func loadPopupEnabled() bool { return loadPopupEnabledFrom(configDir()) }

func savePopupEnabled(on bool) { savePopupEnabledFrom(configDir(), on) }

func loadPopupEnabledFrom(dir string) bool {
	f := popupOffFile(dir)
	if f == "" {
		return true // 没有配置目录 = 用默认值
	}
	_, err := os.Stat(f)
	return os.IsNotExist(err)
}

func savePopupEnabledFrom(dir string, on bool) {
	f := popupOffFile(dir)
	if f == "" {
		return
	}
	if on {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			log.Printf("[config] 重新开启「划词弹出」失败: %v", err)
		}
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("[config] 关闭「划词弹出」失败: %v", err)
		return
	}
	if err := os.WriteFile(f, []byte("off\n"), 0o644); err != nil {
		log.Printf("[config] 关闭「划词弹出」失败: %v", err)
	}
}

// ================================================================ 开启的技能

// skillsFile 里存的是**开启的**技能 id，一行一个，顺序就是工具条上的顺序。
//
// 和「划词弹出」开关一样用"文件存不存在"表达默认值：没有文件 = 全开。
func skillsFile(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "skills.txt")
}

func loadSkillsFrom(dir string) []string {
	f := skillsFile(dir)
	if f == "" {
		return nil
	}
	b, err := os.ReadFile(f)
	if err != nil {
		return nil // 没有文件 / 读失败 = 用默认（全开）
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if id := strings.TrimSpace(line); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func saveSkillsFrom(dir string, ids []string) {
	f := skillsFile(dir)
	if f == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("[config] 保存技能列表失败: %v", err)
		return
	}
	if err := os.WriteFile(f, []byte(strings.Join(ids, "\n")+"\n"), 0o644); err != nil {
		log.Printf("[config] 保存技能列表失败: %v", err)
	}
}

func loadSkills() []string    { return loadSkillsFrom(configDir()) }
func saveSkills(ids []string) { saveSkillsFrom(configDir(), ids) }
