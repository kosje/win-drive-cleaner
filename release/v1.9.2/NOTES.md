修复微软拼音实机 GUID 匹配与还原遗漏、支持 `--optimize --dry-run`、修复短路径临时目录重复统计与 CLI 输入残留。

### 修复与改进

- **修复微软拼音在 Windows 10/11 实机上 GUID 不匹配导致无法真正移除/恢复的 Bug**：原 `.bat` 脚本中 `$pinyin1`/`$pinyin2` 均误写了后半段 GUID，未能命中 Windows 10/11 实机 `Get-WinUserLanguageList` 中的真实微软拼音标识 `0804:{81D4E9C9-1D3B-41BC-9E6C-4B40BF79E35E}{FA550B04-5AD7-411F-A5AC-CA038EC515D7}`。现同时按精确标识与 CLSID/Profile GUID 通配清理（保留第三方输入法），兼容 `zh-Hans-CN` / `zh-CN`，并在仅选方案 2 且中文语言下无其他键盘时自动兜底保留 `0804:00000409` 以防系统自动重置回微软拼音；还原时恢复真实微软拼音 GUID。
- **补齐还原中心遗漏项**：恢复 Windows 更新时自动检测并移除 `WaaSMedicSvc` 的单字节损坏 `FailureActions` 值；恢复 22 项 Windows AI 时补齐 `AcceptedPrivacyPolicy`、`ModelDownloadEnabled`、`CanCortanaBeEnabled` 还原与 `SettingsPageVisibility` 空子键清理，并仅在服务存在时恢复 AI 服务启动类型。
- **支持 `--optimize` 演练模式并前置参数校验**：`--optimize` 现支持 `--dry-run`（只校验参数并打印演练说明，不请求 UAC、不修改系统或删除文件）；CLI 与交互菜单在请求 UAC 提权前先行校验参数合法性，命令行直接调用失败时返回非零退出码。
- **修复 8.3 短路径 `%TEMP%` 与 `%LOCALAPPDATA%\Temp` 重复统计**：`collectItems` 结合 Windows 文件句柄解析后的真实长路径去重，避免在 `%TEMP%` 为短路径的系统上演练模式重复统计同一临时目录。
- **修复 CLI 输入行尾空格残留与 GUI 磁盘容量刷新**：`askYesNo` 与 `askInt` 统一改用 `readInputLine` 整行读取；GUI 执行优化后同步刷新顶栏与磁盘信息页容量，并新增深度优化面板前端单元测试。

### 下载

- `WinDriveCleaner-x64.exe`：64 位 Windows。
- `WinDriveCleaner-x86.exe`：32 位 Windows。
- `WinDriveCleaner-v1.9.2.zip`：两个 EXE、README、LICENSE、审查记录与缓存规则。
- `SHA256SUMS`：三个下载附件的校验值。

支持 Windows 10/11；GUI 需要 WebView2 Runtime，缺失时回退 CLI。深度优化与还原操作需要管理员权限，建议操作后注销或重启生效。
