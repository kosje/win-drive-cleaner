# 代码审查与修复记录

审查对象：`kosje/win-drive-cleaner`。以下修复纳入 `v1.7.1`，随源代码和发布包提供。

## 已修复问题

| 问题及影响 | 修复位置和行为 |
| --- | --- |
| 视频/大文件“打开”遇到含空格的路径时，Go 会为整个 `/select,路径` 参数加引号，Explorer 可能回退到“文档”；逗号也会引入解析歧义 | `explorer.go`：改用 `SHParseDisplayName` 和 `SHOpenFolderAndSelectItems`，打开父目录并选中文件；`gui.go` 将定位放入后台任务，`gui.html` 显示打开失败的原因 |
| `%LOCALAPPDATA%` 等 Windows 环境变量未展开；带引号的启动命令在解析前被转换为绝对路径，破坏命令格式 | `util.go`、`explorer.go`：支持 `%VAR%`，先提取存在的文件路径，再转为绝对路径 |
| 大文件删除在真实盘根后再次追加反斜杠，导致系统盘内正常文件全部被拒绝 | `paths.go`、`bigfiles.go`：统一盘根和子路径判断，支持 Windows 大小写规则 |
| 扫描和删除使用不同的大小写排除规则；扫描也未考虑非默认 Windows 目录 | `bigfiles.go`：统一保护目录和系统保留文件规则，并排除实际 `SystemRoot` |
| 检查最终文件不能识别父目录联接；递归统计可能穿过联接进入清理范围之外 | `paths.go`：检查路径各级祖先的 reparse 属性；清理、缓存、通讯、媒体及残留打包扫描均跳过联接、符号链接和其他 reparse point |
| 深度清理直接删除 `WinSxS\Backup`，与系统组件保护承诺冲突 | `engine.go`：移除裸删条目并拒绝 System32/WinSxS 目标；组件清理由单独的 DISM 功能执行。Microsoft 明确指出直接删除 WinSxS 文件可能破坏系统，[官方说明](https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/clean-up-the-winsxs-folder) |
| Prefetch 错指向盘根；缩略图清理删除整个 Explorer 数据目录；DNS 目录并非有效 DNS 缓存清理目标 | `engine.go`：修正为 `SystemRoot\Prefetch` 并保留目录，仅删除 `thumbcache_*.db`，移除无效 DNS 目录条目 |
| 系统临时目录在深度计划中重复出现，可能重复估算空间 | `engine.go`：删除重复条目，合并相同路径和模式的清理项 |
| `RemoveAll` 部分成功后重复统计，甚至把剩余文件数量当字节数，导致释放空间失真；部分权限错误被忽略 | `engine.go`：逐项删除，仅累加实际成功移除的文件字节；报告访问/占用错误；单文件模式不再误清空目录 |
| GUI 演练修改全局状态，后续显式缓存/大文件删除会继承演练；交互菜单也会留下状态 | `engine.go`、`gui_api.go`、`main.go`：GUI 使用任务独立的演练选项；菜单每次选择复位；显式删除独立于演练状态，日志仅由实际删除分支调用 |
| 多个后台清理任务可能并发修改状态；开始前的停止请求会被 worker 再次重置 | `gui.go`、`engine.go`：后端拒绝重复任务和并发修改任务；仅在接受新清理时重置停止标志，worker 不再丢失取消请求 |
| GUI 后端调用失败时只删除等待者而不 reject 原 Promise，按钮和等待状态可能一直卡住；重复任务会覆盖原等待者 | `gui.html`：同步/异步启动异常都结束原 Promise；同名任务不可覆盖；派发后的窗口关闭状态也再次检查 |
| 磁盘可用空间读取用了 GetDiskFreeSpaceExW 的“总容量”输出 | `disk.go`：读取第一项可用字节数，恢复清理前后空间对比 |
| UAC 失败码 1–32 大多被误认为成功；交互选择深度后，提权进程可能重新进入原 GUI/菜单；自动模式父子进程重复清理通讯文件 | `elevation.go`、`main.go`、`autostart.go`：按 `>32` 判成功，使用当前选择构造参数，正确转义 Windows 参数；父进程提权后停止后续自动清理。[ShellExecuteW 返回值说明](https://learn.microsoft.com/en-us/windows/win32/api/shellapi/nf-shellapi-shellexecutew) |
| Local 和 Roaming 下同名软件仅显示一份；CLI 显示后重新扫描会改变删除序号；空格分隔选择读不完整 | `cache.go`：按完整路径去重，以同一扫描快照显示和删除，读完整输入行，过滤重复序号；大文件 CLI 也使用同一快照定位 |
| 启动文件夹错用 Local 而非 Roaming；禁用后按扩展名过滤导致条目消失，无法恢复；Run/RunOnce 同名值 ID 冲突 | `startup.go`：修正目录，识别禁用后缀后的原扩展名，ID 包含完整注册表来源；所有用户启动文件夹也检查管理员权限 |
| 注册表错误依赖英文文本；访问拒绝被当作“已启用”；RunOnce 的 StartupApproved 标记并不能可靠控制一次性执行 | `startup.go`：使用错误码，正确报告访问失败；RunOnce 显示为只读，后端明确拒绝无效开关，避免返回虚假的操作成功 |
| 日志尾部遇到结尾换行丢失一条记录，且为读几百行加载整个文件 | `log.go`、`gui_api.go`：分块倒读，限制内存，正确返回尾部行数和内容 |
| compact 依赖本地化帮助中的英文词；执行失败只要有输出就报告成功；固定使用 C 盘及旧参数；`--compact --dry-run` 忽略演练 | `compact.go`、`main.go`：使用文档规定的 `/CompactOS:always`，对应解压为 `/CompactOS:never`；识别稳定开关名，正确报告非零退出，保留演练选项。[compact 官方参数](https://learn.microsoft.com/en-us/windows-server/administration/windows-commands/compact) |
| 视频扫描为空时仍保留旧序号；部分交互菜单在 stdin 结束后无限循环 | `media.go`、`main.go`、`startup_cli.go`：清空旧扫描映射，输入结束时返回 |

## 验证结果

- Windows amd64：`go test ./... -count=1`、`go vet ./...` 通过。
- Windows 386：`GOARCH=386 go test ./... -count=1` 通过。
- 20 个 Go 测试入口：19 个默认运行的回归测试，以及 1 个可选真实桌面测试。测试覆盖文件占用导致的部分删除、目录联接、路径边界、状态隔离、启动项恢复及磁盘空间等。
- `node --test gui_test.js`：5 个测试通过，覆盖异步任务错误、重复调用、即时结果和完整脚本语法。
- 真实资源管理器测试通过：含中文、空格、逗号的目录和视频文件名，验证父目录位置及实际文件选中状态。定位使用的 [Windows 原生接口](https://learn.microsoft.com/en-us/windows/win32/api/shlobj_core/nf-shlobj_core-shopenfolderandselectitems) 支持直接打开父目录并选中指定项。
- amd64/386 均成功构建，输出在 `dist/WinDriveCleaner.exe` 和 `dist/WinDriveCleaner_386.exe`。

验证使用隔离临时目录；未执行真实系统清理、DISM、系统压缩或修改真实自启项。`go test -race` 未运行成功：当前环境关闭 CGO 且未配置 C 编译器。旧版 Windows 的运行兼容性未进行实机验证。
