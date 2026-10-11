package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// 版本
const version = "1.8.1"

// 运行参数（由主流程填充）
var (
	noUAC         bool
	elevated      bool
	noPause       bool // 不暂停，直接退出（脚本/CI 场景）
	inInteractive bool // 处于交互菜单内：任务完成后返回菜单，不暂停
)

func main() {
	stdFlag := flag.Bool("standard", false, "执行普通清理")
	deepFlag := flag.Bool("deep", false, "执行深度清理")
	dryRunFlag := flag.Bool("dry-run", false, "演练模式：只统计，不删除")
	verFlag := flag.Bool("version", false, "打印版本")
	noUACFlag := flag.Bool("no-uac", false, "跳过 UAC 提权（以当前权限运行）")
	elevatedFlag := flag.Bool("elevated", false, "内部标记：已提权子进程，结束不暂停")
	noPauseFlag := flag.Bool("no-pause", false, "不暂停直接退出（脚本/批处理场景）")
	scanComms := flag.Bool("scan-comms", false, "扫描通讯软件旧文件(默认30天前，只统计)")
	scanMedia := flag.Bool("scan-media", false, "扫描视频/录屏候选(只提示大小)")
	scanCache := flag.Bool("scan-cache", false, "扫描 AppData 软件缓存大小 Top N(只统计)")
	scanBigFiles := flag.Bool("scan-bigfiles", false, "扫描 ≥500MB 大文件 Top 20(只列不删)")
	scanStartup := flag.Bool("scan-startup", false, "只读列出全部开机自启动项(注册表 Run/RunOnce + 启动文件夹)")
	manageStartupFlag := flag.Bool("manage-startup", false, "交互管理开机自启动项(可逆禁用/启用)")
	compactFlag := flag.Bool("compact", false, "执行系统盘压缩(compact /CompactOS:always, 需管理员, Win10/11)")
	autoFlag := flag.Bool("auto", false, "自动模式：倒计时后按上次设置执行（由登录自启调用，回车可取消）")
	guiFlag := flag.Bool("gui", false, "启动图形界面(GUI, 需 Win10/11 自带 WebView2)")
	showHelp := flag.Bool("h", false, "显示帮助")
	flag.Parse()

	noUAC = *noUACFlag
	elevated = *elevatedFlag
	noPause = *noPauseFlag
	stand, deep, dry, ver, help := *stdFlag, *deepFlag, *dryRunFlag, *verFlag, *showHelp

	switch {
	case ver:
		printVersion()
	case help:
		printHelp()
	case *autoFlag:
		DryRun = dry
		runAutoFlow()
	case *scanComms:
		CommsFlowReadOnly(30)
	case *scanMedia:
		MediaFlowReadOnly()
	case *scanCache:
		CacheFlowReadOnly()
	case *scanBigFiles:
		BigFilesFlowReadOnly()
	case *scanStartup:
		StartupFlowReadOnly()
	case *manageStartupFlag:
		manageStartup()
	case *guiFlag:
		DryRun = dry
		if !runGUI() {
			inInteractive = true
			interactive(noUAC)
		}
	case *compactFlag:
		RunCompact(dry)
	case deep:
		DryRun = dry
		execute(true, noUAC)
	case stand:
		DryRun = dry
		execute(false, noUAC)
	case dry:
		DryRun = true
		execute(false, noUAC)
	default:
		runDefault()
	}
}

// runDefault 双击运行（无任何命令行参数）时的默认入口：
// 在 Win10/11 上默认打开 GUI，并且启动即请求管理员权限；
// WebView2 缺失（Win8 / 早期 Win10）时回退到命令行交互菜单。
func runDefault() {
	// 非管理员且未禁用 UAC：启动即请求管理员权限，提权后直接进入 GUI
	if !noUAC && !isAdmin() {
		if err := elevateSelfWithArgs("--gui"); err == nil {
			// 提权成功：父进程结束，子进程(管理员 + --gui)接管并负责运行 GUI
			return
		}
		// 提权被取消/失败：降级为当前权限继续，不阻塞用户
	}
	// 以当前权限启动 GUI；WebView2 缺失或创建失败时回退交互菜单
	if !runGUI() {
		inInteractive = true
		interactive(noUAC)
	}
}

// execute 按模式执行清理（含 UAC 提权判断）。
func execute(includeDeep, skipUAC bool) bool {
	// 深度清理需管理员；非管理员时自动请求 UAC 提权
	if includeDeep && !skipUAC && !isAdmin() {
		fmt.Println("  深度清理需要管理员权限，正在请求 UAC 提权…")
		var extra []string
		if inInteractive {
			extra = []string{"--deep"}
			if DryRun {
				extra = append(extra, "--dry-run")
			}
		}
		if err := elevateSelfWithArgs(extra...); err != nil {
			fmt.Println("  提权失败:", err)
			// 提权失败也要暂停，让用户看到原因
			pause()
			os.Exit(2)
		}
		// 提权成功：父进程结束，子进程（elevated=true）接管并负责暂停
		return false
	}

	fmt.Printf(" [%s] %s\n", nowStamp(), osDescription())
	if DryRun {
		fmt.Println("  [演练模式] 不会真正删除任何文件")
	}
	fmt.Println()

	// 清理前剩余空间（真实值，用于前后对比）
	freeBefore := freeSpaceOnSystemDrive()

	// 逐条实时进度：每项完成后立即打印“正在/已完成清理什么 + 释放多少”
	OnProgress = func(seq, total int, it CleanItem, res *Result) {
		action := "跳过"
		if res.Deleted > 0 || res.Bytes > 0 {
			action = "已清理"
		} else if res.Errors > 0 {
			action = "部分受限"
		}
		if DryRun {
			action = "预计"
		}
		fmt.Fprintf(os.Stdout, "  [%2d/%d] %-14s %s  %s\n", seq, total, it.Name, action, formatBytes(res.Bytes))
		fmt.Fprintf(os.Stdout, "           └ %s\n", it.Path)
	}

	results := runItems(includeDeep)
	printReport(os.Stdout, results, includeDeep, DryRun)
	OnProgress = nil

	// 清理后剩余空间 + 对比
	if !DryRun {
		freeAfter := freeSpaceOnSystemDrive()
		if freeBefore >= 0 && freeAfter >= 0 {
			fmt.Fprintf(os.Stdout, "  磁盘剩余空间: %s  ->  %s\n", formatBytes(freeBefore), formatBytes(freeAfter))
			if freeAfter > freeBefore {
				fmt.Fprintf(os.Stdout, "  实测释放: %s\n", formatBytes(freeAfter-freeBefore))
			} else {
				fmt.Fprintf(os.Stdout, "  （文件删除后磁盘计数可能略有延迟，数值以“已释放”统计为准）\n")
			}
		}
	}

	if !DryRun && includeDeep {
		// 一键清空回收站已在深度项完成；这里把"删不掉的残留临时文件"压缩成一个 zip，供用户手动转移/删除
		if packPath, n, err := PackLeftovers(); err != nil {
			fmt.Printf("  残留文件打包失败: %v\n", err)
		} else if n > 0 {
			fmt.Println()
			fmt.Printf("  已把 %d 个删不掉的残留临时文件压缩为: %s\n", n, packPath)
			fmt.Println("  （可手动转移或删除该压缩包；压缩包本身不计入释放空间）")
		}
		fmt.Println()
		fmt.Println("  深度清理已完成。以下可选进阶项（需管理员）：")
		fmt.Println("   ① DISM 组件清理：DISM /Online /Cleanup-Image /StartComponentCleanup")
		fmt.Println("      （清理 WinSxS 组件备份，通常可再释放数百 MB 到数 GB，但耗时较长）")
		fmt.Println("   ② 通讯软件旧文件 / 视频录屏：请在交互菜单选对应功能")
	}

	// 交互菜单内：不暂停，直接返回菜单，方便连续做下一项；只有选 q 才退出
	// 命令行直接执行（非交互）：默认暂停方便看清结果；--no-pause 时自动结束
	if inInteractive {
		return true
	}
	if !noPause {
		pause()
	}
	return true
}

// pause 停住等待回车，避免窗口一闪而过。
func pause() {
	fmt.Println()
	fmt.Print("  按回车键退出…")
	var dummy string
	_, _ = fmt.Scanln(&dummy)
}

func interactive(skipUAC bool) {
	for {
		fmt.Println()
		fmt.Println("================ Win 盘清理工具 ================")
		fmt.Printf(" 系统: %s\n", osDescription())
		fmt.Printf(" 权限: %s\n", adminStateLabel())
		fmt.Println()
		fmt.Println("  选择模式:")
		fmt.Println("   1) 普通清理")
		fmt.Println("   2) 深度清理(需管理员)")
		fmt.Println("   3) 演练普通清理(不删除)")
		fmt.Println("   4) 演练深度清理(不删除)")
		fmt.Println("   5) 清理通讯软件旧文件(微信/QQ/企业微信)")
		fmt.Println("   6) 查看 视频/录屏(仅提示大小，可打开所在文件夹手动删)")
		fmt.Println("   7) DISM 组件清理(可选进阶)")
		fmt.Println("   8) 本工具登录自启(倒计时自动清理/取消/关闭自启)")
		fmt.Println("   9) 软件缓存(识别已知缓存目录, 保留配置及登录数据)")
		fmt.Println("  10) 大文件检测(≥500MB, Top20, 只列不删)")
		fmt.Println("  11) 系统盘压缩(compact LZX, 需管理员, Win10/11)")
		fmt.Println("  12) 开机自启动管理(可逆禁用/启用 Run 项与启动文件夹)")
		fmt.Println("  13) 图形界面 GUI(需 Win10/11 自带 WebView2)")
		fmt.Println("   q) 退出")
		fmt.Print("  请输入: ")

		var choice string
		_, err := fmt.Scanln(&choice)
		if err != nil && choice == "" {
			return
		}
		choice = strings.ToLower(strings.TrimSpace(choice))
		DryRun = false
		switch choice {
		case "1":
			DryRun = false
			execute(false, skipUAC)
		case "2":
			DryRun = false
			execute(true, skipUAC)
		case "3":
			DryRun = true
			execute(false, skipUAC)
		case "4":
			DryRun = true
			execute(true, skipUAC)
		case "5":
			commsFlow()
		case "6":
			mediaFlow()
		case "7":
			RunDismCleanup(false)
		case "8":
			manageAutostart()
		case "9":
			cacheFlow()
		case "10":
			bigFileFlow()
		case "11":
			RunCompact(false)
		case "12":
			manageStartup()
		case "13":
			runGUI()
		case "q", "exit", "quit":
			fmt.Println("  已退出。")
			return
		default:
			fmt.Println("  未识别的选项，请重新输入。")
			continue
		}
		// 做完一项，打印分隔并自动回到菜单，可继续做下一项
		fmt.Println("\n  —— 已返回主菜单，可继续选择其他功能，输入 q 退出 ——")
	}
}

// commsFlow 通讯软件清理流程：先按天数扫描显示大小，再确认删除。
func commsFlow() {
	DryRun = false
	days := askInt("  清理多少天前的通讯软件文件？(默认 30, 0=全部旧文件也保留只统计): ", 30)
	if days < 0 {
		days = 0
	}
	PrintCommsScan(days)
	if days == 0 {
		fmt.Println("  [0] 只统计，不删除。")
		return
	}
	if !askYesNo(fmt.Sprintf("  确定删除 %d 天前的通讯旧文件吗（附件不可恢复，可能无法再次查看）？", days), false) {
		fmt.Println("  已取消，未删除任何通讯文件。")
		return
	}
	for _, t := range commsTargets() {
		b, n, e := deleteCommsOlder(t, days)
		if b == 0 && n == 0 {
			continue
		}
		fmt.Printf("   %-18s 已删除 %d 个文件，释放 %s%s\n", t.Name, n, formatBytes(b), errNote(int(e)))
	}
}

// mediaFlow 视频/录屏流程：显示大小，用户可选打开所在文件夹手动清理。
func mediaFlow() {
	printMediaScan(0) // 默认 Top 10
	if len(mediaIndex) == 0 {
		return
	}
	for {
		fmt.Println("  输入 M 序号(1-10)打开所在文件夹，o=全部仅查看，q=返回")
		var c string
		_, scanErr := fmt.Scanln(&c)
		if scanErr != nil && c == "" {
			return
		}
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "q" {
			return
		}
		if c == "o" {
			// 打开第一个候选所在目录（仅示例）
			if len(mediaIndex) > 0 {
				_ = OpenInExplorer(filepath.Dir(mediaIndex[0].Path))
			}
			continue
		}
		n, err := strconv.Atoi(c)
		if err != nil {
			continue
		}
		openMediaFolder(n)
	}
}

// errNote 当有错误时返回简短提示，否则返回空串。
func errNote(e int) string {
	if e > 0 {
		return fmt.Sprintf("（%d 项因占用未删）", e)
	}
	return ""
}

func printVersion() {
	fmt.Println("Win 盘清理工具  v" + version)
}

func printHelp() {
	fmt.Println("Win 盘清理工具  v" + version)
	fmt.Println("  清理 Windows 系统盘垃圾，零安装、绿色单文件，支持 Windows 10/11。")
	fmt.Println()
	fmt.Println("用法:")
	fmt.Println("  直接运行              启动图形界面，缺少 WebView2 时进入交互菜单")
	fmt.Println("  --standard            普通清理(低风险)")
	fmt.Println("  --deep                深度清理(需管理员, 释放空间更多)")
	fmt.Println("  --dry-run             演练: 只统计大小, 不删除")
	fmt.Println("  --no-uac              跳过 UAC 提权")
	fmt.Println("  --no-pause            不暂停直接退出(脚本/批处理场景)")
	fmt.Println("  --scan-comms          扫描通讯软件(微信/QQ/企业微信)旧文件, 只统计")
	fmt.Println("  --scan-media          扫描视频/录屏候选, 只提示大小")
	fmt.Println("  --scan-cache          扫描 AppData 软件缓存大小 Top N, 只统计")
	fmt.Println("  --scan-bigfiles       扫描 ≥500MB 大文件 Top 20, 只列不删")
	fmt.Println("  --scan-startup        只读列出全部开机自启动项(注册表 Run/RunOnce + 启动文件夹)")
	fmt.Println("  --manage-startup      交互管理开机自启动项(可逆禁用/启用, 非本工具自启)")
	fmt.Println("  --compact             执行系统盘压缩(compact LZX, 需管理员, Win10/11)")
	fmt.Println("  --gui                 启动图形界面(需 Win10/11 自带 WebView2)")
	fmt.Println("  --auto                自动模式: 倒计时后按上次设置执行(本工具登录自启调用)")
	fmt.Println("  --version             打印版本")
	fmt.Println("  --help                本帮助")
	fmt.Println()
	fmt.Println("交互菜单另有:")
	fmt.Println("   5) 清理通讯软件旧文件(可指定 N 天)   6) 查看视频/录屏")
	fmt.Println("   7) DISM 组件清理(可选进阶)")
	fmt.Println("   8) 本工具登录自启: 开关登录自启、设倒计时、选普通/深度、关闭下次自启")
	fmt.Println("   9) 软件缓存: 识别已知缓存目录, 保留配置及登录数据")
	fmt.Println("  10) 大文件检测: ≥500MB Top20, 只列不删, 可打开所在文件夹")
	fmt.Println("  11) 系统盘压缩: compact LZX 就地压缩 C:\\Windows, 释放 1-3GB (Win10/11, 可逆)")
	fmt.Println("  12) 开机自启动管理: 可逆禁用/启用 注册表 Run/RunOnce 与启动文件夹(非本工具自启)")
	fmt.Println("  13) 图形界面 GUI: 需 Win10/11 自带 WebView2")
	fmt.Println()
	fmt.Println("说明: 每次清理结束后默认停在“按回车键退出”，方便看清结果；")
	fmt.Println("      脚本自动化时加 --no-pause 可自动结束。")
}

func adminStateLabel() string {
	if isAdmin() {
		return "管理员"
	}
	return "普通用户"
}

// CommsFlowReadOnly 只读扫描通讯软件旧文件（--scan-comms）。
func CommsFlowReadOnly(days int) {
	PrintCommsScan(days)
}

// MediaFlowReadOnly 只读扫描视频/录屏候选（--scan-media）。
func MediaFlowReadOnly() {
	printMediaScan(0)
}
