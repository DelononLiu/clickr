//go:build windows

// hook_test.go —— 手势识别 与 采集服务（常驻单线程 / 忙时丢弃）。
//
// 交叉编译后直接跑：
//
//	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o clickr.test.exe .
//	./clickr.test.exe -test.v

package main

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestDragSelectIsRecognised(t *testing.T) {
	// 从左往右拖 120px，明显是一次框选
	got := driveHookEvents(t, []hookEvent{
		{kind: evDown, pt: point{500, 400}},
		{kind: evUp, pt: point{620, 402}},
	})
	if len(got) != 1 || got[0] != "drag" {
		t.Fatalf("期望识别出 1 次 drag，实际 %v", got)
	}
}

func TestTinyMoveIsNotASelection(t *testing.T) {
	// 移动 2px 属于手抖，不是划词（阈值 5px）
	got := driveHookEvents(t, []hookEvent{
		{kind: evDown, pt: point{500, 400}},
		{kind: evUp, pt: point{502, 401}},
	})
	if len(got) != 0 {
		t.Fatalf("2px 抖动不该算划词，实际 %v", got)
	}
}

func TestPlainClickIsNotASelection(t *testing.T) {
	got := driveHookEvents(t, []hookEvent{
		{kind: evDown, pt: point{500, 400}},
		{kind: evUp, pt: point{500, 400}},
	})
	if len(got) != 0 {
		t.Fatalf("单击不该算划词，实际 %v", got)
	}
}

func TestDoubleClickSelectsWord(t *testing.T) {
	// 同一个点快速点两下 = 双击选词
	got := driveHookEvents(t, []hookEvent{
		{kind: evDown, pt: point{300, 300}},
		{kind: evUp, pt: point{300, 300}},
		{kind: evDown, pt: point{301, 300}},
		{kind: evUp, pt: point{301, 300}},
	})
	if len(got) != 1 || got[0] != "double-click" {
		t.Fatalf("期望识别出 1 次 double-click，实际 %v", got)
	}
}

func TestTwoFarApartClicksAreNotDoubleClick(t *testing.T) {
	// 两次单击离得很远，不该被当成双击
	got := driveHookEvents(t, []hookEvent{
		{kind: evDown, pt: point{100, 100}},
		{kind: evUp, pt: point{100, 100}},
		{kind: evDown, pt: point{900, 700}},
		{kind: evUp, pt: point{900, 700}},
	})
	if len(got) != 0 {
		t.Fatalf("两处远距离单击不该合并成双击，实际 %v", got)
	}
}

func TestUpWithoutDownIsIgnored(t *testing.T) {
	// 程序启动前就按下的那次抬起，不该触发
	got := driveHookEvents(t, []hookEvent{
		{kind: evUp, pt: point{500, 400}},
	})
	if len(got) != 0 {
		t.Fatalf("孤立的上抬事件不该触发，实际 %v", got)
	}
}

// 回归测试：采集服务的「忙碌判据」曾经用 atomic.Bool 表达，
// 零值是 false，而判据写成 CompareAndSwap(true, false)（要求当前为 true），
// 于是**第一次采集就被跳过、之后每一次都被跳过** —— 整个程序的取词功能全废，
// 而界面上只会静默地不弹菜单。
//
// 这个测试断言「第一个请求必须被处理」，那条 bug 会被当场抓住。
func TestCaptureServiceHandlesFirstRequest(t *testing.T) {
	var calls atomic.Int32
	svc := newCaptureService(
		func(captureRequest) captureResult {
			calls.Add(1)
			return captureResult{ok: true, text: "第一个请求的文本", method: "fake"}
		},
		func(captureResult) {},
	)
	svc.start()
	time.Sleep(60 * time.Millisecond) // 等 worker 阻塞在 channel 接收上

	res, got := svc.submit(captureRequest{how: "test"}, 500*time.Millisecond)
	if !got {
		t.Fatal("第一个请求就被跳过了 —— 忙碌判据写错了（这正是 captureIdle 那个 bug 的症状）")
	}
	if !res.ok || res.text != "第一个请求的文本" {
		t.Errorf("结果不对: ok=%v text=%q", res.ok, res.text)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("worker 被调用 %d 次，期望 1 次", n)
	}
}

// 采集线程正忙（模拟卡在某个程序的延迟渲染上）时，新请求必须被**立刻丢弃**，
// 而不是排队 —— 排队没有意义（等它腾出手用户早选了别的），
// 更要紧的是绝不能出现两次采集并发操作同一个剪贴板。
func TestCaptureServiceDropsWhenBusy(t *testing.T) {
	var block atomic.Bool
	release := make(chan struct{})
	var calls atomic.Int32

	svc := newCaptureService(
		func(captureRequest) captureResult {
			calls.Add(1)
			if block.Load() {
				<-release
			}
			return captureResult{ok: true, text: "T", method: "fake"}
		},
		func(captureResult) {},
	)
	svc.start()
	time.Sleep(60 * time.Millisecond)

	block.Store(true)
	// 这条会卡在 work 里，所以放后台跑
	go func() { _, _ = svc.submit(captureRequest{how: "blocking"}, 200*time.Millisecond) }()

	// 等 worker 真的进入阻塞态
	deadline := time.Now().Add(1 * time.Second)
	for calls.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if calls.Load() < 1 {
		close(release)
		t.Fatal("worker 没有开始处理请求")
	}

	if _, ok := svc.submit(captureRequest{how: "while-busy"}, 150*time.Millisecond); ok {
		close(release)
		t.Error("采集线程忙时不应接受新请求（会导致两次采集并发操作剪贴板）")
	}

	close(release)
	// 放开之后应当恢复接活
	block.Store(false)
	time.Sleep(150 * time.Millisecond)
	if _, ok := svc.submit(captureRequest{how: "after"}, 500*time.Millisecond); !ok {
		t.Error("采集线程空闲后应当能继续接活")
	}
}
