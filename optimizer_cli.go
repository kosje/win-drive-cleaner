//go:build windows

package main

import (
	"fmt"
	"strings"
)

// RunOptimizeCLI 通过命令行参数执行单项深度优化/还原（--optimize）。
func RunOptimizeCLI(action, preset string) {
	res, err := OptimizeExecute(action, preset)
	if err != nil {
		fmt.Println("  错误:", err)
		return
	}
	if out, _ := res["output"].(string); out != "" {
		fmt.Println(out)
	}
	if msg, _ := res["msg"].(string); msg != "" {
		fmt.Println("  " + msg)
	}
}

// manageOptimize 命令行交互式深度优化与净化菜单。
func manageOptimize() {
	for {
		fmt.Println()
		fmt.Println("========== Windows 深度优化与净化（需管理员） ==========")
		fmt.Printf(" 权限: %s\n", adminStateLabel())
		fmt.Println("   1) 彻底删除微软拼音输入法 + 永久免疫 (可选美式键盘方案)")
		fmt.Println("   2) 彻底封杀 Windows 系统更新 (停用服务/破坏自愈/策略拦截)")
		fmt.Println("   3) 彻底删除 Windows AI 全特性 (对标 RemoveWindowsAI 22 项)")
		fmt.Println("   4) 【一键终极精简】拼音免疫 + 封杀更新 + 删除 22 项 AI + 深度清理 C 盘")
		fmt.Println("   5) 还原：恢复微软拼音输入法")
		fmt.Println("   6) 还原：恢复 Windows 系统更新")
		fmt.Println("   7) 还原：恢复全部 22 项 Windows AI 支持")
		fmt.Println("   q) 返回主菜单")
		fmt.Print("  请输入: ")

		choice, err := readInputLine()
		if err != nil && choice == "" {
			return
		}
		choice = strings.ToLower(strings.TrimSpace(choice))
		switch choice {
		case "1":
			preset := askPinyinPreset()
			RunOptimizeCLI(OptActionPurgePinyin, preset)
		case "2":
			if askYesNo("  确定彻底封杀 Windows 系统更新吗（可随时在此菜单还原）?", false) {
				RunOptimizeCLI(OptActionBlockUpdate, "")
			}
		case "3":
			if askYesNo("  确定彻底删除 22 项 Windows AI 特性吗（可随时在此菜单还原）?", false) {
				RunOptimizeCLI(OptActionRemoveAI, "")
			}
		case "4":
			preset := askPinyinPreset()
			if askYesNo("  确定执行一键终极精简（拼音 + 更新 + AI + 深度清理 C 盘）吗?", false) {
				RunOptimizeCLI(OptActionOptimizeAll, preset)
			}
		case "5":
			RunOptimizeCLI(OptActionRestorePinyin, "")
		case "6":
			RunOptimizeCLI(OptActionRestoreUpdate, "")
		case "7":
			RunOptimizeCLI(OptActionRestoreAI, "")
		case "q", "exit", "quit":
			return
		default:
			fmt.Println("  未识别的选项，请重新输入。")
		}
	}
}

func askPinyinPreset() string {
	fmt.Println("  请选择移除微软拼音后保留的美式键盘方案：")
	fmt.Println("    [1] 保留【简体中文 - 美式键盘】(0804:00000409)（推荐）")
	fmt.Println("    [2] 保留/添加【英语(美国) - 普通美式键盘】(en-US)")
	fmt.Println("    [3] 两者同时配置（双重保障）")
	fmt.Print("  请输入方案编号 [1-3] (直接回车默认 1): ")
	line, _ := readInputLine()
	line = strings.TrimSpace(line)
	if line != "1" && line != "2" && line != "3" {
		return "1"
	}
	return line
}
