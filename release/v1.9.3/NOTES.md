最终轮代码审查修复：同步通知服务控制管理器（SCM）使恢复更新即时生效，并补齐 Windows 设置页隐藏官方策略路径。

### 修复与改进

- **修复不重启直接恢复 Windows 更新时 `wuauserv` 启动失败的问题**：此前封杀/恢复更新仅修改服务注册表 `Start` 值，Windows 服务控制管理器（SCM）在重启前不会重载该值，导致封杀后立即点击“恢复 Windows 系统更新”时 `Start-Service wuauserv` 失败。现同步调用 `Set-Service` 通知 SCM 立即生效。
- **补齐 Windows 设置页隐藏官方组策略路径**：在保留原路径的同时，同步将 `hide:ai-components;recall;copilot` 写入 Windows 10/11 实际读取的 `HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\Explorer\SettingsPageVisibility`（仅在未配置自定义策略时写入，还原时仅在值匹配时移除，不覆盖用户原有配置）。

### 下载

- `WinDriveCleaner-x64.exe`：64 位 Windows。
- `WinDriveCleaner-x86.exe`：32 位 Windows。
- `WinDriveCleaner-v1.9.3.zip`：两个 EXE、README、LICENSE、审查记录与缓存规则。
- `SHA256SUMS`：三个下载附件的校验值。

支持 Windows 10/11；GUI 需要 WebView2 Runtime，缺失时回退 CLI。深度优化与还原操作需要管理员权限，建议操作后注销或重启生效。
