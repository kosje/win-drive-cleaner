//go:build windows

package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/jchv/go-webview2"
)

//go:embed gui.html
var guiHTML string

// guiApp 持有 WebView 句柄与生命周期标志。
//
// 线程模型（重要）：go-webview2 的 Bind 回调是在 **UI 消息线程上同步**执行的。
// 若在绑定里直接跑全盘扫描 / DISM / 压缩，整个窗口会失去响应（连关闭按钮都点不动）。
// 因此本文件把所有耗时操作统一下放给 app.async()：绑定函数立即返回，
// 真正的活儿在后台 goroutine 跑，完成后通过 window.__wdcResult 异步回传。
type guiApp struct {
	w       webview2.WebView
	closed  atomic.Bool
	mu      sync.Mutex
	running map[string]bool
	scans   map[string]context.CancelFunc
}

// jsStr 把字符串编码成安全的 JS 字符串字面量（含引号与转义）。
func jsStr(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// eval 在 UI 线程执行一段 JS。窗口已关闭时直接丢弃——
// 否则会向已销毁的 WebView2 COM 对象派发调用，导致进程崩溃。
func (a *guiApp) eval(js string) {
	if a == nil || a.w == nil || a.closed.Load() {
		return
	}
	a.w.Dispatch(func() {
		if !a.closed.Load() {
			a.w.Eval(js)
		}
	})
}

// pushResult 把异步任务结果送回前端：window.__wdcResult(task, {ok,data,error})。
func (a *guiApp) pushResult(task string, data interface{}, err error) {
	payload := map[string]interface{}{"ok": err == nil, "data": data, "error": "", "cancelled": errors.Is(err, context.Canceled)}
	if err != nil {
		payload["error"] = err.Error()
		if errors.Is(err, context.Canceled) {
			payload["error"] = "扫描已取消"
		}
	}
	b, merr := json.Marshal(payload)
	if merr != nil {
		b, _ = json.Marshal(map[string]interface{}{"ok": false, "data": nil, "error": "结果序列化失败"})
	}
	a.eval("window.__wdcResult(" + jsStr(task) + "," + string(b) + ")")
}

// async 在后台 goroutine 执行 job 并把结果推回前端。
// 内置 panic 恢复：单个任务出错绝不能带崩整个 GUI 进程。
func (a *guiApp) async(task string, job func() (interface{}, error)) (map[string]interface{}, error) {
	return a.startAsync(task, false, func(_ *scanControl) (interface{}, error) { return job() })
}

func (a *guiApp) asyncScan(task string, job func(*scanControl) (interface{}, error)) (map[string]interface{}, error) {
	return a.startAsync(task, true, job)
}

func (a *guiApp) startAsync(task string, scanning bool, job func(*scanControl) (interface{}, error)) (map[string]interface{}, error) {
	a.mu.Lock()
	if a.running == nil {
		a.running = make(map[string]bool)
	}
	mutating := task == "clean" || task == "cacheDelete" || task == "bigfilesDelete" || task == "dism" || task == "compact"
	if a.closed.Load() || a.running[task] || (mutating && a.running["mutation"]) {
		a.mu.Unlock()
		return nil, fmt.Errorf("操作正在执行或窗口已关闭，请稍后重试")
	}
	a.running[task] = true
	if mutating {
		a.running["mutation"] = true
	}
	if task == "clean" {
		ResetCleanCancel()
	}
	var control *scanControl
	if scanning {
		ctx, cancel := context.WithCancel(context.Background())
		if a.scans == nil {
			a.scans = make(map[string]context.CancelFunc)
		}
		a.scans[task] = cancel
		control = &scanControl{ctx: ctx, notify: func(progress ScanProgress) {
			a.pushResult("scanProgress", map[string]interface{}{"task": task, "visited": progress.Visited,
				"errors": progress.Errors, "path": progress.Path}, nil)
		}}
	}
	a.mu.Unlock()
	go func() {
		var data interface{}
		var err error
		defer func() {
			if r := recover(); r != nil {
				data, err = nil, fmt.Errorf("内部错误: %v", r)
			}
			a.mu.Lock()
			if cancel, ok := a.scans[task]; ok {
				cancel()
				delete(a.scans, task)
			}
			delete(a.running, task)
			if mutating {
				delete(a.running, "mutation")
			}
			a.mu.Unlock()
			a.pushResult(task, data, err)
		}()
		data, err = job(control)
	}()
	return map[string]interface{}{"started": true, "task": task}, nil
}

func (a *guiApp) cancelScan(task string) (map[string]interface{}, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cancel, ok := a.scans[task]
	if !ok {
		return nil, fmt.Errorf("该扫描已结束")
	}
	cancel()
	return map[string]interface{}{"ok": true}, nil
}

func (a *guiApp) close() {
	a.closed.Store(true)
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, cancel := range a.scans {
		cancel()
	}
}

// runGUI 启动 WebView2 原生窗口并承载图形界面。
// 返回是否成功启动并运行完 GUI（创建失败返回 false，供调用方回退到交互菜单）。
func runGUI() bool {
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug: false,
		WindowOptions: webview2.WindowOptions{
			Title:  "Win 盘清理",
			Width:  1180,
			Height: 800,
			Center: true,
		},
	})
	if w == nil {
		fmt.Println("  GUI 启动失败：未能创建 WebView2 窗口。")
		fmt.Println("  原因通常是系统缺少 WebView2 运行时（Win10/21H2+ 通常已自带；")
		fmt.Println("  Windows 10/11 缺少运行时时需安装 Microsoft Edge WebView2 Runtime）。")
		fmt.Println("  已回退到命令行交互菜单。")
		return false
	}

	app := &guiApp{w: w}
	defer w.Destroy()

	// ---------- 快操作：直接在 UI 线程执行（毫秒级注册表/磁盘查询）----------
	_ = w.Bind("apiDiskInfo", func() (map[string]interface{}, error) {
		return app.async("diskInfo", func() (interface{}, error) {
			return apiDiskInfo()
		})
	})
	_ = w.Bind("apiStartupList", apiStartupList)
	_ = w.Bind("apiStartupToggle", apiStartupToggle)
	_ = w.Bind("apiOpenFolder", func(path string) (map[string]interface{}, error) {
		return app.async("openFolder", func() (interface{}, error) {
			return nil, apiOpenFolder(path)
		})
	})
	_ = w.Bind("apiCancelClean", apiCancelClean)
	_ = w.Bind("apiCancelScan", app.cancelScan)
	_ = w.Bind("apiElevate", apiElevate)

	// ---------- 耗时任务：立即返回，结果经 window.__wdcResult 异步回传 ----------

	// 删除审计日志读取（文件可能很大，走异步）
	_ = w.Bind("apiReadLog", func() (map[string]interface{}, error) {
		return app.async("readLog", func() (interface{}, error) {
			return apiReadLog(400)
		})
	})

	// 磁盘清理（含实时逐项进度推送）
	_ = w.Bind("apiClean", func(deep, dry bool) (map[string]interface{}, error) {
		return app.async("clean", func() (interface{}, error) {
			return runCleanJob(app, deep, dry)
		})
	})

	// 通讯软件缓存扫描
	_ = w.Bind("apiCommsScan", func() (map[string]interface{}, error) {
		return app.asyncScan("comms", scanCommsJob)
	})

	// AppData 软件缓存扫描
	_ = w.Bind("apiCacheScan", func() (map[string]interface{}, error) {
		return app.asyncScan("cache", scanCacheJob)
	})

	// 软件缓存删除：前端回传**完整路径**（而非序号），后端做白名单校验后删除
	_ = w.Bind("apiCacheDelete", func(paths []string) (map[string]interface{}, error) {
		return app.async("cacheDelete", func() (interface{}, error) {
			return deleteCacheJob(paths)
		})
	})

	// 大文件检测
	_ = w.Bind("apiBigFiles", func() (map[string]interface{}, error) {
		return app.asyncScan("bigfiles", scanBigFilesJob)
	})

	// 大文件多选删除：前端回传完整路径，后端白名单校验后删除
	_ = w.Bind("apiBigFilesDelete", func(paths []string) (map[string]interface{}, error) {
		return app.async("bigfilesDelete", func() (interface{}, error) {
			return deleteBigFilesJob(paths)
		})
	})

	// 视频 / 录屏扫描
	_ = w.Bind("apiMediaScan", func() (map[string]interface{}, error) {
		return app.asyncScan("media", scanMediaJob)
	})

	// DISM 组件清理（可能耗时数分钟，必须异步）
	_ = w.Bind("apiDism", func() (map[string]interface{}, error) {
		return app.async("dism", func() (interface{}, error) {
			return DismExecute(false)
		})
	})

	// 系统盘压缩（耗时，必须异步）
	_ = w.Bind("apiCompact", func() (map[string]interface{}, error) {
		return app.async("compact", func() (interface{}, error) {
			return CompactExecute(false)
		})
	})

	w.SetHtml(guiHTML)
	w.Run()

	// 消息循环退出 = 窗口已销毁。此后禁止任何 Dispatch/Eval，
	// 否则后台扫描任务会向已释放的 WebView2 对象派发调用，把进程带崩。
	app.close()
	return true
}
