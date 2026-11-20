新增 Windows 深度优化与净化模块（微软拼音免疫、封杀系统更新、删除 22 项 Windows AI、一键终极精简与还原中心）并扩展 C 盘清理项。

### 新增功能与改进

- **微软拼音彻底删除 + 永久免疫**：移除微软拼音 GUID 与注册表残留，阻断账户语言同步与后台 FOD 自动重装任务，支持保留「简体中文 - 美式键盘」「英语(美国) - 美式键盘」或双重配置。
- **彻底封杀 Windows 系统更新**：停用并锁定 `wuauserv` / `WaaSMedicSvc` / `UsoSvc` / `bits` / `dosvc`，阻断 WaaSMedic 自愈机制，写入组策略拦截并禁用全部更新唤醒计划任务。
- **彻底删除 Windows AI 全特性（22 项）**：对标 RemoveWindowsAI，覆盖终止后台 AI 进程、禁用 Copilot / Recall / AI Fabric / 输入洞察 / Edge 侧边栏 Copilot / 画图与记事本 AI / 语音模型下载 / 联网 AI 搜索，卸载相关 AppX 与 CBS 组件包，隐藏设置页并清理本地缓存。
- **一键终极精简与分类还原中心**：支持按序执行「拼音免疫 + 封杀更新 + 删除 22 项 AI + 深度清理 C 盘垃圾」；提供分类还原中心，可一键恢复微软拼音、Windows 系统更新或全部 22 项 AI 支持。
- **C 盘清理项扩展**：新增用户级 WER、CrashDumps、DirectX/NVIDIA/AMD 着色器缓存、`iconcache_*.db`、传递优化缓存、Minidump、`MEMORY.DMP` 与 CBS/DISM/DPX/Panther 诊断安装日志清理。
- **GUI + CLI 双端同步支持**：GUI 新增「深度优化与净化」导航页与实时输出日志；CLI 新增交互菜单 `14)` 与 `--optimize` / `--pinyin-preset` 参数。

### 下载

- `WinDriveCleaner-x64.exe`：64 位 Windows。
- `WinDriveCleaner-x86.exe`：32 位 Windows。
- `WinDriveCleaner-v1.9.0.zip`：两个 EXE、README、LICENSE、审查记录与缓存规则。
- `SHA256SUMS`：三个下载附件的校验值。

支持 Windows 10/11；GUI 需要 WebView2 Runtime，缺失时回退 CLI。深度优化与还原操作需要管理员权限，建议操作后注销或重启生效。
