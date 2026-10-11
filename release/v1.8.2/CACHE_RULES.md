# 软件缓存规则

仅扫描以下已知的软件及缓存子目录。软件根目录、浏览器配置、Cookies、Login Data、Local Storage、IndexedDB、扩展、聊天数据和下载文件均不属于缓存删除白名单。未知软件不会因目录名包含 Cache 而自动获得删除资格。

| 软件 | 缓存位置 |
| --- | --- |
| Chrome | `%LOCALAPPDATA%\Google\Chrome\User Data\Default\{Cache,Code Cache,GPUCache}`，及 `Profile <数字>` 下同名目录 |
| Edge | `%LOCALAPPDATA%\Microsoft\Edge\User Data` 下同样的配置目录及缓存子目录 |
| Brave | `%LOCALAPPDATA%\BraveSoftware\Brave-Browser\User Data` 下同样的配置目录及缓存子目录 |
| Firefox | `%LOCALAPPDATA%\Mozilla\Firefox\Profiles\*\cache2` |
| VS Code / Insiders | `%APPDATA%\Code` / `%APPDATA%\Code - Insiders` 下的 `Cache`、`Code Cache`、`GPUCache`、`CachedData` |
| Discord / PTB / Canary | `%APPDATA%\discord` / `discordptb` / `discordcanary` 下的 `Cache`、`Code Cache`、`GPUCache` |
| Slack | `%APPDATA%\Slack` 下的 `Cache`、`Code Cache`、`GPUCache` |

清理会保留缓存目录本身，仅移除其内容；已占用或无法访问的文件会跳过。建议先关闭对应软件，避免清理过程中重新生成缓存。客户端版本或自定义数据位置可能不同；未匹配的缓存请在对应软件内清理。

规则依据包括 [Electron 的用户数据与 Chromium 子目录说明](https://www.electronjs.org/docs/latest/api/app#appgetpathname)、[Chromium 缓存路径实现](https://chromium.googlesource.com/chromium/src/+/master/chrome/common/chrome_paths.cc)和 [Firefox cache2 文档](https://github.com/mozilla-firefox/firefox/blob/main/netwerk/docs/cache2/doc.md)。具体应用的目录映射由本项目维护，新增规则需同时验证缓存删除和相邻持久数据保留。
