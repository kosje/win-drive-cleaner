本版修复了取消 UAC 时的错误提示，并加固 GUI 日志目录定位。

### 功能与修复

- **修复取消 UAC 的错误提示**：`ShellExecuteW` 通过返回值而非 errno 报告失败，用户取消 UAC 提权时返回 `SE_ERR_ACCESSDENIED`(5)；原实现误判 `ERROR_CANCELLED`(1223)，导致取消提示退化为泛化的「启动提权进程失败 (code=5)」。现正确识别为「你已取消 UAC 权限请求」，并补充回归测试。
- **日志目录定位改用后端权威路径**：GUI「打开日志目录」此前硬编码 `%LOCALAPPDATA%` 字面量、依赖后端环境变量展开；现改为采用读取日志时后端返回的真实目录，消除对环境变量展开的隐式依赖。

### 下载

- `WinDriveCleaner-x64.exe`：64 位 Windows。
- `WinDriveCleaner-x86.exe`：32 位 Windows。
- `WinDriveCleaner-v1.8.1.zip`：两个 EXE、README、LICENSE、审查记录和缓存规则。
- `SHA256SUMS`：三个下载附件的 SHA256 校验值。

当前构建支持 Windows 10/11，GUI 需要 WebView2 Runtime，缺失时回退 CLI。建议先关闭对应软件再清理缓存；占用或无法访问的文件会跳过。

### 验证范围

本次改动涉及 UAC 提权返回值判断与 GUI 前端日志目录读取；已通过双架构编译、Go vet、前端脚本测试及 UAC 取消回归测试。完整审查记录见 `REVIEW.md`。
