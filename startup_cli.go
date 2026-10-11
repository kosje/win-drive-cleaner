package main

import (
	"fmt"
	"strconv"
	"strings"
)

// 开机自启动管理 —— CLI 交互 + 只读扫描（打印层，与 startup.go 纯逻辑分离）。

// PrintStartupScan 只读列出全部开机自启动项。
func PrintStartupScan() {
	items := enumerateStartups()
	if len(items) == 0 {
		fmt.Println("  未发现开机自启动项。")
		return
	}
	fmt.Printf("\n  共发现 %d 个开机自启动项（权限: %s）:\n", len(items), adminStateLabel())
	for i, it := range items {
		state := "启用"
		if !it.Enabled {
			state = "禁用"
		}
		fmt.Printf("   %2d. [%s] %s\n", i+1, state, it.Name)
		if it.Target != "" {
			fmt.Printf("        目标: %s\n", it.Target)
		}
		fmt.Printf("        来源: %s\n", it.Source)
	}
	fmt.Println("\n  用法: d<序号> 禁用 / e<序号> 启用 / r 重新列表 / a 全部禁用 / q 返回")
}

// manageStartup 交互：列出全部自启动项，逐个 禁用/启用，可逆。
func manageStartup() {
	items := enumerateStartups()
	if len(items) == 0 {
		fmt.Println("  未发现可管理的开机自启动项。")
		return
	}

	admin := isAdmin()
	refreshStartup(items, admin)

	for {
		fmt.Print("\n  输入: d<序号>禁用 / e<序号>启用 / r重新列表 / a全部禁用 / q返回: ")
		var c string
		_, err := fmt.Scanln(&c)
		if err != nil && c == "" {
			return
		}
		c = strings.ToLower(strings.TrimSpace(c))
		switch {
		case c == "q" || c == "exit":
			return
		case c == "r":
			items = enumerateStartups()
			refreshStartup(items, admin)
		case c == "a":
			confirmAllDisable(items, admin)
			items = enumerateStartups()
			refreshStartup(items, admin)
		case strings.HasPrefix(c, "d"), strings.HasPrefix(c, "e"):
			enable := c[0] == 'e'
			idx, err := strconv.Atoi(c[1:])
			if err != nil || idx < 1 || idx > len(items) {
				fmt.Println("  无效序号。")
				continue
			}
			it := items[idx-1]
			if it.Source.needsAdmin() && !admin {
				fmt.Println("  该项属「所有用户(HKLM)」，需以管理员身份运行本工具才能操作。")
				continue
			}
			res := setValueEnabled(&it, enable)
			if res.OK {
				fmt.Printf("  → %s  [%s]\n", res.Msg, it.Name)
			} else {
				fmt.Printf("  → 失败: %s\n", res.Error)
			}
			items = enumerateStartups()
			refreshStartup(items, admin)
		default:
			// 直接输数字：默认禁用
			if idx, err := strconv.Atoi(c); err == nil && idx >= 1 && idx <= len(items) {
				it := items[idx-1]
				if it.Source.needsAdmin() && !admin {
					fmt.Println("  该项属「所有用户(HKLM)」，需以管理员身份运行本工具才能操作。")
					continue
				}
				res := setValueEnabled(&it, false)
				if res.OK {
					fmt.Printf("  → %s  [%s]\n", res.Msg, it.Name)
				} else {
					fmt.Printf("  → 失败: %s\n", res.Error)
				}
				items = enumerateStartups()
				refreshStartup(items, admin)
				continue
			}
			refreshStartup(items, admin)
		}
	}
}

// refreshStartup 打印当前自启动列表。
func refreshStartup(items []StartupEntry, admin bool) {
	for i, it := range items {
		state := "启用"
		if !it.Enabled {
			state = "禁用"
		}
		tag := ""
		if it.Source.needsAdmin() && !admin {
			tag = "（需管理员，当前跳过）"
		} else if it.Source.isLMHive() {
			tag = "（HKLM）"
		}
		fmt.Printf("   %2d. [%s] %-26s %s\n", i+1, state, it.Name, tag)
		if it.Target != "" {
			fmt.Printf("        目标: %s   [%s]\n", it.Target, it.Source)
		}
	}
}

// confirmAllDisable 逐个确认并禁用当前权限可管理的全部启用项。
func confirmAllDisable(items []StartupEntry, admin bool) {
	n := 0
	for i := range items {
		it := items[i]
		if !it.Enabled {
			continue
		}
		if it.Source.needsAdmin() && !admin {
			continue // 无权限项跳过
		}
		fmt.Printf("  即将禁用: %s  [%s]  确认? (y/n) ", it.Name, it.Source)
		var ans string
		_, _ = fmt.Scanln(&ans)
		if strings.ToLower(ans) != "y" {
			continue
		}
		res := setValueEnabled(&it, false)
		if res.OK {
			n++
			fmt.Printf("    → %s\n", res.Msg)
		} else {
			fmt.Printf("    → 失败: %s\n", res.Error)
		}
	}
	fmt.Printf("  完成：本次禁用 %d 项。\n", n)
}

// StartupFlowReadOnly 供 --scan-startup 直接调用。
func StartupFlowReadOnly() {
	PrintStartupScan()
}
