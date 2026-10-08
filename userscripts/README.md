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

- 点击发送时，脚本会通过 `GM_xmlhttpRequest` 下载 E-Hentai / ExHentai / EHTracker 的实际 `.torrent` 文件，在本地解析 Bencode 元数据，并对完整的 `info` 字典原始字节计算 SHA-1，生成准确的 BTIH 磁链。**不再将下载 URL 中的标识误当成 BTIH**。
- 如果种子为纯 BT v2、下载失败、返回登录 HTML 或文件无法解析，会提示错误并停止，不会向 Bridge 提交错误磁链。支持直接传入带 40 位 BTIH 的现成磁链。
- 发送前调用 `GET {Bridge地址}/api/v1/targets` 获取当前可用目标。只有一个目标时自动选择并发送；多个目标时弹出名称及目录列表，**默认选中上次成功提交时用过的目标**。若没有历史记录或原目标已禁用/删除，则优先选中 Bridge 中配置的默认目标，最后回退到列表第一个目标。选项不再展示“（默认）”字样。
- 上次成功提交使用的目标 ID 由用户脚本管理器保存在本地（`GM_setValue`），**按 Bridge 服务地址分别记忆**，重新打开页面后仍有效。取消选择、提交失败或命中已有任务（HTTP 409）不会覆盖上次使用的记录。
- `POST {Bridge地址}/api/v1/tasks`，请求体为 `{"url":"magnet:?...","target":"选中的目标 ID"}`。目标 ID 无需手工记录或保存在脚本设置中。
- 种子快捷弹窗的 **✂ 剪刀按钮**使用与 Bridge 推送相同的 torrent 二进制解析和 BTIH 计算逻辑，复制真实磁链到剪贴板；失败时弹出错误，不会静默忽略。
- HTTP **201** 显示“已提交”；HTTP **409** 携带 `existing_task_id` 时显示“已存在”，不会重复创建。
- 其他状态、网络异常或无法提取 BTIH 时显示失败原因。
- 第一版**不在 E-Hentai 页面显示 PikPak/aria2 桥接进度**；可在 pikpak-bridge Web UI 中查看任务。原脚本的 aria2 进度显示不变。
- 存档直链（`hath.network/archive`）**仍只发送至原 aria2**，不走 PikPak Bridge。

## 网络权限和安全

新增 `@connect *`，以便用户配置局域网 IP、Tailscale 地址或自定义 HTTPS 域名；部分脚本管理器会弹出跨域权限提示。请**只安装可信的本仓库脚本**，并确认填写的是自己的桥接服务地址。

**当前 pikpak-bridge API 尚未内置鉴权。请勿直接将服务端口暴露到公网。** 可部署在可信局域网内，或通过受控的 HTTPS 入口及鉴权访问；从 HTTPS 页面调用 HTTP 局域网地址是否允许，取决于浏览器和脚本管理器的跨域策略。

## 来源和授权

- 原始作者：xioxin、SchneeHertz；原始项目：[EhTagTranslation/UserScripts](https://github.com/EhTagTranslation/UserScripts)
- 上游版本：EhAria2 v1.2；本地修改版：v1.3.4
- 衍生脚本保留原作者署名并依照 **GNU GPL v3** 分发，许可证全文见 [LICENSE](LICENSE)。此许可声明针对本目录派生的用户脚本，不改变 `pikpak-bridge` 其他独立代码的授权。
- 后续可通过脚本头部的 `@updateURL` / `@downloadURL` 从本仓库同步更新。

## 验证

`node --check userscripts/eharia2-pikpak-bridge.user.js` 和 `node --test userscripts/eharia2-pikpak-bridge.test.cjs`；GitHub Actions CI 会自动运行。
