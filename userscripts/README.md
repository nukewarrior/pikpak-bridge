# EhAria2 + PikPak Bridge 下载助手

本目录提供基于 [EhAria2下载助手 v1.2](https://github.com/EhTagTranslation/UserScripts/blob/master/AriaEh/AriaEh.user.js) 的修改版用户脚本，保留原脚本将 E-Hentai / ExHentai 种子及存档链接发送到 aria2、查看 aria2 下载进度等功能，并为**种子磁链**新增发送到 [pikpak-bridge](../README.md) 的入口。

## 安装

1. 使用支持 `GM_xmlhttpRequest` 的脚本管理器（例如 Tampermonkey、Violentmonkey，或支持该 API 的 Safari Userscripts）。
2. 打开 [eharia2-pikpak-bridge.user.js](eharia2-pikpak-bridge.user.js) 的 Raw 页面并安装。
3. **禁用已安装的原版 EhAria2**，避免两个脚本同时运行导致界面重复。该修改版使用独立的脚本名称和命名空间；原版配置通常需要在此版本中重新填写。
4. 在脚本管理器菜单中打开“设置”，填写：
   - **Bridge 地址**：例如 `http://192.168.1.10:8080`；不要附加 `/api/v1/tasks`。
   - **无需填写下载目标 ID**。每次点击发送时，脚本通过 `GET /api/v1/targets` 实时读取 Bridge 中已启用的下载目标；只有一个目标就直接提交，多个目标会弹出选择框。
   - **Aria2 RPC/密钥/保存路径**：需要保留直接 aria2 下载时填写；只使用 PikPak Bridge 时 RPC 可以留空。
5. 在 Gallery 种子下载页点击“发送到 PikPak”；或在种子快捷弹窗点击青色的 **P** 按钮。原来的 aria2 和复制磁链按钮仍然保留。

> 已配置的 pikpak-bridge 服务需能从当前浏览器所在设备访问，且已完成首次初始化并配置至少一个下载目标。脚本只会发送**磁链**，不会把 .torrent 二进制文件传给后端。

## 工作方式

- 从原脚本能识别的种子 URL 提取 40 位 BTIH Hash，构造 `magnet:?` 磁链。
- 发送前调用 `GET {Bridge地址}/api/v1/targets` 获取当前可用目标。如果只有一个目标则直接选中并发送；如果有多个，则显示名称、目录及默认标记供选择；取消选择不会提交任务。
- `POST {Bridge地址}/api/v1/tasks`，请求体为 `{"url":"magnet:?...","target":"选中的目标 ID"}`。目标 ID 无需手工记录或保存在脚本设置中。
- HTTP **201** 显示“已提交”；HTTP **409** 携带 `existing_task_id` 时显示“已存在”，不会重复创建。
- 其他状态、网络异常或无法提取 BTIH 时显示失败原因。
- 第一版**不在 E-Hentai 页面显示 PikPak/aria2 桥接进度**；可在 pikpak-bridge Web UI 中查看任务。原脚本的 aria2 进度显示不变。
- 存档直链（`hath.network/archive`）**仍只发送至原 aria2**，不走 PikPak Bridge。

## 网络权限和安全

新增 `@connect *`，以便用户配置局域网 IP、Tailscale 地址或自定义 HTTPS 域名；部分脚本管理器会弹出跨域权限提示。请**只安装可信的本仓库脚本**，并确认填写的是自己的桥接服务地址。

**当前 pikpak-bridge API 尚未内置鉴权。请勿直接将服务端口暴露到公网。** 可部署在可信局域网内，或通过受控的 HTTPS 入口及鉴权访问；从 HTTPS 页面调用 HTTP 局域网地址是否允许，取决于浏览器和脚本管理器的跨域策略。

## 来源和授权

- 原始作者：xioxin、SchneeHertz；原始项目：[EhTagTranslation/UserScripts](https://github.com/EhTagTranslation/UserScripts)
- 上游版本：EhAria2 v1.2；本地修改版：v1.3.1
- 衍生脚本保留原作者署名并依照 **GNU GPL v3** 分发，许可证全文见 [LICENSE](LICENSE)。此许可声明针对本目录派生的用户脚本，不改变 `pikpak-bridge` 其他独立代码的授权。
- 后续可通过脚本头部的 `@updateURL` / `@downloadURL` 从本仓库同步更新。

## 验证

`node --check userscripts/eharia2-pikpak-bridge.user.js` 和 `node --test userscripts/eharia2-pikpak-bridge.test.cjs`；GitHub Actions CI 会自动运行。
