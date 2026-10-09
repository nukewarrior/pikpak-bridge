// ==UserScript==
// @name         EhPikPakAria2下载助手
// @namespace    https://github.com/nukewarrior/pikpak-bridge/userscripts
// @version      1.3.15
// @description  保留 EhAria2 功能，新增将 E-Hentai/ExHentai 磁链推送至 pikpak-bridge
// @author       xioxin, SchneeHertz; pikpak-bridge contributors
// @homepage     https://github.com/nukewarrior/pikpak-bridge
// @supportURL   https://github.com/nukewarrior/pikpak-bridge/issues
// @source       https://github.com/EhTagTranslation/UserScripts/blob/master/AriaEh/AriaEh.user.js
// @license      GPL-3.0-only
// @updateURL    https://raw.githubusercontent.com/nukewarrior/pikpak-bridge/master/userscripts/eharia2-pikpak-bridge.user.js
// @downloadURL  https://raw.githubusercontent.com/nukewarrior/pikpak-bridge/master/userscripts/eharia2-pikpak-bridge.user.js
// @include      *://exhentai.org/*
// @include      *://e-hentai.org/*
// @include      *hath.network/archive/*
// @require      https://openuserjs.org/src/libs/sizzle/GM_config.js
// @grant        GM_registerMenuCommand
// @grant        GM_xmlhttpRequest
// @grant        GM_addValueChangeListener
// @grant        GM_removeValueChangeListener
// @grant        GM_setClipboard
// @grant        GM_getValue
// @grant        GM_setValue
// @grant        GM.getValue
// @grant        GM.setValue
// @connect      localhost
// @connect      127.0.0.1
// @connect      *
// ==/UserScript==

// Based on EhTagTranslation/UserScripts AriaEh v1.2, licensed under GPL-3.0.
// Modifications: optional pikpak-bridge submission alongside the original aria2 UI.


const IS_EX = window.location.host.includes("exhentai");
const gmc = new GM_config({
    'id': 'AriaEhSetting',
    'title': 'AriaEh设置',
    'fields': {
        'ARIA2_RPC': {
            'section': [ 'ARIA2配置', '使用 Aria2 下载时再选择保存目录，并自动记住最近使用的位置。如果下载服务器不是本机，请在设置 - XHR 安全 - 用户域名白名单中允许服务器地址。'],
            'label': 'ARIA2_RPC地址（可选）',
            'title': 'ARIA2_RPC地址, 例如: http://127.0.0.1:6800/jsonrpc',
            'labelPos': 'left',
            'type': 'text',
            'default': ''
        },
        'ARIA2_SECRET': {
            'label': 'ARIA2_RPC密钥',
            'title': 'ARIA2_RPC密钥',
            'type': 'text',
            'default': ''
        },
        'BRIDGE_URL': {
            'section': ['PikPak Bridge', '将种子对应的磁链提交给 pikpak-bridge。下载时自动读取可用目标，无需在脚本中填写目标 ID。与原 Aria2 下载互不影响。'],
            'label': 'Bridge 地址',
            'title': '例如：http://192.168.1.10:8080 或 https://bridge.example.com；不包含 /api/v1/tasks',
            'labelPos': 'left',
            'type': 'text',
            'default': ''
        },
        'USE_ONE_CLICK_DOWNLOAD': {
            'section': [ '一键下载存档', '该功能是将"存档下载"的连接发送给Aria2.在列表页面与详情页增加橙色下载按钮.<b style="color: #f60">注意该功能会产生下载费用!</b>'],
            'labelPos': 'left',
            'label': '启用',
            'title': '在列表页与详情页增加存档一键下载按钮',
            'type': 'checkbox',
            'default': true
        },
        'ONE_CLICK_DOWNLOAD_DLTYPE': {
            'label': '一键下载画质',
            'type': 'select',
            'labelPos': 'left',
            'options': ['org(原始档案)', 'res(重采样档案)'],
            'default': 'org(原始档案)'
        },
        'USE_TORRENT_POP_LIST': {
            'section': [ '种子下载快捷弹窗', '鼠标指向详情页的"种子下载",或者列表的绿色箭头.将显示种子列表浮窗.并高亮最大体积,最新更新.' ],
            'labelPos': 'left',
            'label': '启用',
            'title': '使用种子下载快捷弹窗',
            'type': 'checkbox',
            'default': true
        },
        'REPLACE_EX_TORRENT_URL': {
            'label': '里站使用表站种子连接',
            'title': '替换里站种子域名为ehtracker.org',
            'type': 'checkbox',
            'default': true
        },
        'USE_MAGNET': {
            'label': '使用磁力链替代种子链接',
            'title': '先将种子转换为磁力链，再发送给Aria2',
            'type': 'checkbox',
            'default': false
        },
        'USE_LIST_TASK_STATUS': {
            'section': [ '下载进度展示'],
            'labelPos': 'left',
            'label': '在搜索列表页',
            'title': '在搜索列表页',
            'type': 'checkbox',
            'default': true
        },
        'USE_GALLERY_DETAIL_TASK_STATUS': {
            'label': '在画廊详情页',
            'title': '在画廊详情页',
            'type': 'checkbox',
            'default': true
        },
        'USE_HATH_ARCHIVE_TASK_STATUS': {
            'label': '在存档下载页面',
            'title': '在存档下载页面',
            'type': 'checkbox',
            'default': true
        },
        'USE_TORRENT_TASK_STATUS': {
            'label': '在种子下载页面',
            'title': '在种子下载页面',
            'type': 'checkbox',
            'default': true
        },
        'INITIALIZED': {
            'type': 'hidden',
            'default': false,
        }
    },
    'events': {
        'init': onConfigInit
    },
    css: `
    #AriaEhSetting { background: #E3E0D1; }
    #AriaEhSetting .config_header { margin-bottom: 8px; }
    #AriaEhSetting .section_header { font-size: 12pt; }
    #AriaEhSetting .section_header_holder { margin-top: 16pt; }
    #AriaEhSetting input, #AriaEhSetting select { background:#E3E0D1; border: 2px solid #B5A4A4; border-radius: 3px; }
    #AriaEhSetting .field_label { display: inline-block; min-width: 150px; text-align: right;}
    ${IS_EX ? `
    #AriaEhSetting { background:#4f535b; color: #FFF; }
    #AriaEhSetting .section_header { border: 1px solid #000;  }
    #AriaEhSetting .section_desc { background:#34353b; border: 1px solid #000; color: #CCC; }
    #AriaEhSetting input, #AriaEhSetting select { background:#34353b; color: #FFF; border: 2px solid #8d8d8d; border-radius: 3px; }
    #AriaEhSetting_resetLink { color: #FFF; }
    `: ''}
    `
})
console.log('gmc', gmc);

function onConfigInit() {
    // 如果没有配置地址, 在首页弹出配置页面
    if(!gmc.get('ARIA2_RPC') && !gmc.get('BRIDGE_URL') && window.location.pathname === '/') {
        gmc.open();
        const frame = document.getElementById('AriaEhSetting');
        if(frame) frame.style.cssText = iframeCss;
    }
    init()
}

const iframeCss = `
    width: min(440px, 95vw);
    height: min(640px, 85vh);
    border: 1px solid;
    border-radius: 4px;
    position: fixed;
    z-index: 9999;
`

GM_registerMenuCommand("设置", () => {
    gmc.open()
    AriaEhSetting.style = iframeCss
})


let ARIA2_CLIENT_ID = GM_getValue('ARIA2_CLIENT_ID', '');
if (!ARIA2_CLIENT_ID) {
    ARIA2_CLIENT_ID = "EH-" + new Date().getTime();
    GM_setValue("ARIA2_CLIENT_ID", ARIA2_CLIENT_ID);
}

const IS_TORRENT_PAGE = window.location.href.includes("gallerytorrents.php");
const IS_HATH_ARCHIVE_PAGE = window.location.href.includes("hath.network/archive");
const IS_GALLERY_DETAIL_PAGE = window.location.href.includes("/g/");

const STYLE = `
.aria2helper-box {
    min-height: 27px;
    height: auto;
    line-height: 27px;
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 4px;
}
.aria2helper-bridge-button {
    cursor: pointer;
}
.aria2helper-one-click.bt-bridge-button {
    background: radial-gradient(#68d1c4,#218b83);
    color: white;
    font-weight: bold;
    font-size: 11px;
}
.aria2helper-one-click.bt-bridge-button:hover {
    background: radial-gradient(#66e0d1,#12665f);
}
.aria2helper-button { }
.aria2helper-loading { }
.aria2helper-message { cursor: pointer;  }
.aria2helper-status {
    display: none;
    padding: 4px 4px;
    font-size: 12px;
    text-align: center;
    background: rgba(${IS_EX ? '0,0,0': '255,255,255'}, 0.6);
    margin: 4px 8px;
    border-radius: 4px;
    font-weight: normal;
    white-space: normal;
    box-shadow: 0 1px 3px rgb(0 0 0 / 30%);
}
.glname .aria2helper-status {
    margin: 4px 0px;
}
.gl3e .aria2helper-status {
    margin: 0px 4px;
    padding: 4px 4px;
    width: 112px !important;
    white-space: normal;
    box-sizing: border-box;
    text-align: center !important;
}
.gl3e .aria2helper-status span {
    display: block;
}
.gl1t .aria2helper-status{
    margin: 4px 4px;
}

/* Reserve space in the site's table cell; otherwise width:100% compresses
 * both columns to ~70px even when the desired grid width is 232px. */
#torrentinfo td.aria2helper-torrent-actions-cell {
    box-sizing: border-box;
    width: 248px;
    min-width: 248px;
    padding: 8px;
    vertical-align: middle;
}
#torrentinfo .aria2helper-torrent-actions-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    grid-template-rows: 38px 38px;
    gap: 8px;
    width: 232px;
    min-width: 232px;
    margin: 8px auto 3px;
    box-sizing: border-box;
}
#torrentinfo .aria2helper-torrent-actions-grid > .aria2helper-box {
    grid-column: 1 / -1;
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 8px;
    min-height: 0;
    height: 38px;
    line-height: normal;
    margin: 0;
}
#torrentinfo .aria2helper-torrent-actions-grid > .aria2helper-box > .aria2helper-button,
#torrentinfo .aria2helper-torrent-actions-grid > .aria2helper-box > .aria2helper-bridge-button,
#torrentinfo .aria2helper-torrent-actions-grid > .aria2helper-native-action {
    box-sizing: border-box;
    display: block;
    width: 100%;
    min-width: 0;
    min-height: 38px;
    height: 38px;
    margin: 0;
    padding: 5px 4px;
    border: 1px solid #b3a5a5;
    border-radius: 6px;
    text-align: center;
    font-size: 12px;
    line-height: 1.45;
    white-space: nowrap;
    cursor: pointer;
}
#torrentinfo .aria2helper-torrent-actions-grid > .aria2helper-box > .aria2helper-button {
    grid-column: 1;
    grid-row: 1;
    background: #548f30;
    border-color: #477c26;
    color: #fff;
}
#torrentinfo .aria2helper-torrent-actions-grid > .aria2helper-box > .aria2helper-bridge-button {
    grid-column: 2;
    grid-row: 1;
    background: #188f83;
    border-color: #13766c;
    color: #fff;
}
#torrentinfo .aria2helper-torrent-actions-grid > .aria2helper-box > .aria2helper-loading,
#torrentinfo .aria2helper-torrent-actions-grid > .aria2helper-box > .aria2helper-message {
    grid-column: 1;
    grid-row: 1;
    align-self: center;
    min-width: 0;
}
#torrentinfo .aria2helper-torrent-actions-grid > .aria2helper-native-action {
    grid-row: 2;
    background: rgba(160, 160, 160, 0.12);
    color: inherit;
    white-space: normal;
    text-decoration: none;
}
#torrentinfo .aria2helper-torrent-actions-grid > .aria2helper-native-copy {
    grid-column: 1;
}
#torrentinfo .aria2helper-torrent-actions-grid > .aria2helper-native-info {
    grid-column: 2;
}
/* Keep site-native Copy Magnet and Information controls equal-width,
 * even when site input styles specify a narrower width or float. */
#torrentinfo .aria2helper-torrent-actions-grid > .aria2helper-native-action {
    display: block !important;
    justify-self: stretch !important;
    width: 100% !important;
    max-width: none !important;
    float: none !important;
}
#torrentinfo .aria2helper-torrent-actions-grid > .aria2helper-native-action:hover {
    background: rgba(160, 160, 160, 0.25);
}
/* Override the site's .stdbtn:hover appearance to preserve white text contrast. */
#torrentinfo .aria2helper-torrent-actions-grid > .aria2helper-box > .aria2helper-button:hover,
#torrentinfo .aria2helper-torrent-actions-grid > .aria2helper-box > .aria2helper-button:focus-visible {
    background: #3c7022 !important;
    border-color: #315d1b !important;
    color: #fff !important;
}
#torrentinfo .aria2helper-torrent-actions-grid > .aria2helper-box > .aria2helper-bridge-button:hover,
#torrentinfo .aria2helper-torrent-actions-grid > .aria2helper-box > .aria2helper-bridge-button:focus-visible {
    background: #11756e !important;
    border-color: #0d5e58 !important;
    color: #fff !important;
}

/* Bridge status is independent of the existing direct-aria2 status widgets. */
.aria2helper-bridge-progress {
    display: none;
    box-sizing: border-box;
    min-width: 0;
    margin: 5px 4px;
    padding: 6px 8px;
    background: rgba(120, 150, 150, 0.12);
    border: 1px solid rgba(120, 150, 150, 0.35);
    border-radius: 6px;
    color: inherit;
    font: 11px/1.45 system-ui, sans-serif;
    overflow-wrap: anywhere;
}
.aria2helper-bridge-progress-label { display: block; text-align: left; }
.aria2helper-bridge-progress-track {
    display: none;
    height: 4px;
    margin-top: 5px;
    background: rgba(128, 128, 128, 0.3);
    border-radius: 5px;
    overflow: hidden;
}
.aria2helper-bridge-progress-fill {
    display: block;
    width: 0;
    height: 100%;
    background: #168f83;
    border-radius: inherit;
}
.aria2helper-bridge-progress[data-state="failed"] { border-color: #bf625e; }
.aria2helper-bridge-progress[data-state="completed"] {
    padding: 3px 7px;
    border-color: rgba(84, 143, 48, 0.45);
    background: rgba(84, 143, 48, 0.1);
}
.aria2helper-bridge-progress[data-state="completed"] .aria2helper-bridge-progress-track {
    display: none !important;
}
#torrentinfo .aria2helper-bridge-progress { width: 232px; margin: 7px auto 3px; }
#btList .bt-actions-cell .aria2helper-bridge-progress {
    max-width: 160px;
    margin: 5px 0 0;
    padding: 3px 5px;
    white-space: normal;
    text-align: left;
}
/* Extended-list metadata (.gl3e) uses positioned children; render in the content cell instead. */
.gl4e > .aria2helper-bridge-progress {
    position: static;
    float: none;
    clear: both;
    width: fit-content;
    max-width: calc(100% - 8px);
    margin: 8px 4px 4px;
    vertical-align: top;
}
.glname .aria2helper-bridge-progress { margin: 4px 0; }
`;


const ONE_CLICK_STYLE = `
.aria2helper-one-click {
    width: 15px;
    height: 15px;
    background: radial-gradient(#ffc36b,#c56a00);
    border-radius: 15px;
    border: 1px #666 solid;
    box-sizing: border-box;
    color: #ebeae9;
    text-align: center;
    line-height: 15px;
    cursor: pointer;
    user-select: none;
}
.aria2helper-one-click:hover {
    background: radial-gradient(#bf893b,#985200);
}
.aria2helper-one-click.bt {
    background: radial-gradient(#a2d04f,#5fb213);
}
.aria2helper-one-click.bt:hover {
    background: radial-gradient(#95cf2b,#427711);
}
.aria2helper-one-click i {
    font-style: initial;
    transform: scale(0.7);
    margin-left: -1.5px;
}
.gldown {
    width: 35px !important;
    display: flex;
    flex-direction: row;
    justify-content: space-between;
}
.gl3e>div:nth-child(6) {
    left: 45px;
}
.aria2helper-one-click svg circle {
    stroke: #fff !important;
    stroke-width: 15px !important;
}
.aria2helper-one-click svg {
    width: 10px;
    display: inline-block;
    height: 10px;
    padding-top: 1.3px;
}
.gsp .aria2helper-one-click {
    display: inline-block;
    margin-left: 8px;
    vertical-align: -1.5px;
}

#gd5 .g2 {
    position: relative;
}
#btList{
    display: none;
    background: #f00;
    width: 90%;
    position: absolute;
    border-radius: 4px;
    border: 1px;
    z-index: 999;
    padding: 8px 0;
    font-size: 12px;
    text-align: left;
    background: rgba(${IS_EX ? '0,0,0': '255,255,255'}, 0.6);
    border-radius: 4px;
    font-weight: normal;
    white-space: normal;
    box-shadow: 0 1px 3px rgb(0 0 0 / 30%);
    backdrop-filter: saturate(180%) blur(20px);
    font-size: 12px;
    width: max-content;
    margin-top: 8px;
}
.nowrap {
    white-space:nowrap;
}
#gmid #btList {
    right: 0;
    margin-top: 16px;
}
.gldown #btList{
    left: 0;
}
.gldown #btList table {
    max-width: 60vw;
}
.btListShow #btList{
    display: block;
}
#btList .bt-item {
    padding: 4px 8px;
}
#btList .bt-name {
    font-weight: bold;
}
#btList .quality {
    font-weight: bold;
}
#btList td span {
    display: inline-block;
    padding: 2px 4px;
    height: 16px;
    line-height: 16px;
}
#btList td span.quality {
    font-weight: bold;
    border-radius: 4px;
    background: ${IS_EX ? '#fff': '#5c0d12'};
    color:  ${IS_EX ? '#000': '#fff'};
}

#btList table {
    border-spacing:0;
    border-collapse:collapse;
    max-width: 80vw;
}
#btList tr th {
    padding-bottom: 8px;
    text-align: center;
}
#btList tr th span {
    font-weight: 400;
}
#btList tr td {
    padding: 2px 4px;
}
#btList tr:hover td {
    background: rgba(${IS_EX ? '0,0,0': '255,255,255'}, 0.6);
}
#btList tr>td:first-of-type, #btList tr>th:first-of-type {
    padding: 0 8px;
}
#btList tr>td:last-child, #btList tr>th:last-child {
    padding-right: 8px;
}

/* Torrent actions share one table cell, avoiding column-specific spacing. */
#btList tr td.bt-actions-cell {
    padding: 2px 8px;
    white-space: nowrap;
    vertical-align: middle;
}
#btList .bt-actions-group {
    display: inline-flex;
    flex-direction: row;
    align-items: center;
    justify-content: flex-start;
    gap: 8px;
    white-space: nowrap;
    vertical-align: middle;
}
#btList .bt-actions-group .aria2helper-one-click {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex: 0 0 18px;
    width: 18px;
    height: 18px;
    margin: 0;
    padding: 0;
    box-sizing: border-box;
    border-radius: 50%;
    font-size: 12px;
    line-height: 1;
    vertical-align: middle;
    text-align: center;
}

`;

const SVG_LOADING_ICON = `<svg style="margin: auto; display: block; shape-rendering: auto;" width="24px" height="24px" viewBox="0 0 100 100" preserveAspectRatio="xMidYMid">
<circle cx="50" cy="50" fill="none" stroke="${IS_EX ? '#f1f1f1': '#5C0D11'}" stroke-width="10" r="35" stroke-dasharray="164.93361431346415 56.97787143782138">
  <animateTransform attributeName="transform" type="rotate" repeatCount="indefinite" dur="1s" values="0 50 50;360 50 50" keyTimes="0;1"></animateTransform>
</circle></svg>`;

const ARIA2_ERROR_MSG = {
    '2': '操作超时',
    '3': '无法找到指定资源',
    '4': "无法找到指定资源.",
    '5': "由于下载速度过慢, 下载已经终止.",
    '6': "网络问题",
    '8': "服务器不支持断点续传",
    '9': "可用磁盘空间不足",
    '10': "分片大小与 .aria2 控制文件中的不同.",
    '11': "aria2 已经下载了另一个相同文件.",
    '12': "aria2 已经下载了另一个相同哈希的种子文件.",
    '13': "文件已经存在.",
    '14': "文件重命名失败.",
    '15': "文件打开失败.",
    '16': "文件创建或删除已有文件失败.",
    '17': "文件系统出错.",
    '18': "无法创建指定目录.",
    '19': "域名解析失败.",
    '20': "解析 Metalink 文件失败.",
    '21': "FTP 命令执行失败.",
    '22': "HTTP 返回头无效或无法识别.",
    '23': "指定地址重定向过多.",
    '24': "HTTP 认证失败.",
    '25': "解析种子文件失败.",
    '26': '指定 ".torrent" 种子文件已经损坏或缺少 aria2 需要的信息.',
    '27': '指定磁链地址无效.',
    '28': '设置错误.',
    '29': '远程服务器繁忙, 无法处理当前请求.',
    '30': '处理 RPC 请求失败.',
    '32': '文件校验失败.'
};


class AriaClientLite {
    constructor(opt = {}) {
        this.rpc = opt.rpc;
        this.secret = opt.secret;
        this.id = opt.id;
    }

    async addUri(url, dir = '') {
        const response = await this.post(this.rpc, this._addUriParameter(url, dir));
        return this.singleResponseGuard(response);
    }

    async tellStatus(id) {
        const response = await this.post(this.rpc, this._tellStatusParameter(id));
        return this.singleResponseGuard(response);
    }

    async batchTellStatus(ids = []) {
        if(!ids.length) return [];
        const dataList = ids.map(id => this._tellStatusParameter(id));
        const response = await this.post(this.rpc, dataList);
        if(response.responseType !== 'json') throw `不支持的数据格式: ${response.status}`;
        const json = JSON.parse(response.responseText);
        if(!Array.isArray(json)) throw "批量请求数据结构错误";
        return json.map(v => v.result);
    }

    request(url, opt={}) {
        return new Promise((resolve, reject) => {
            opt.onerror = opt.ontimeout = reject
            opt.onload = resolve
            GM_xmlhttpRequest({
                url,
                timeout: 2000,
                responseType: 'json',
                ...opt
            });
        })
    }

    post(url, data = {}) {
        return this.request(url, {
            method: "POST",
            data: JSON.stringify(data),
        });
    }

    singleResponseGuard(response) {
        if(response.responseType !== 'json') {
            throw `不支持的数据格式: ${response.status}`;
        }
        const json = JSON.parse(response.responseText);
        if(response.status !== 200 && json && json.error) throw `${json.error.code} ${json.error.message}`;
        if(response.status !== 200) throw `错误: ${response.status}`;
        return json.result;
    }

    _jsonRpcPack(method, params) {
        if (this.secret) params.unshift("token:" + this.secret);
        return {
            "jsonrpc": "2.0",
            "method": method,
            "id": ARIA2_CLIENT_ID,
            params
        }
    }

    _addUriParameter(url, dir = '') {
        const opt = {"follow-torrent": 'true'};
        if(dir) opt['dir'] = dir;
        return this._jsonRpcPack('aria2.addUri', [ [url], opt ]);
    }

    _tellStatusParameter(id) {
        return this._jsonRpcPack('aria2.tellStatus', [id]);
    }

}

const BRIDGE_TARGET_STYLE = `
.aria2helper-bridge-overlay {
    position: fixed;
    inset: 0;
    z-index: 2147483646;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 16px;
    background: rgba(0, 0, 0, 0.55);
}
.aria2helper-bridge-dialog {
    box-sizing: border-box;
    width: min(560px, 100%);
    max-height: 85vh;
    overflow-y: auto;
    border-radius: 10px;
    padding: 20px;
    color: #222;
    background: #fff;
    box-shadow: 0 12px 36px rgba(0,0,0,.3);
    font: 14px/1.5 system-ui, sans-serif;
}
.aria2helper-bridge-dialog h3 {
    margin: 0 0 10px;
    color: #222;
    font-size: 18px;
    font-weight: 600;
}
.aria2helper-bridge-dialog p {
    margin: 0 0 12px;
    color: #555;
}
.aria2helper-bridge-dialog .aria2helper-file-info {
    display: flex;
    align-items: center;
    gap: 12px;
    min-width: 0;
    margin: 14px 0 18px;
    padding: 13px;
    border: 1px solid #d8e4e6;
    border-radius: 8px;
    background: #f7fafb;
}
.aria2helper-bridge-dialog .aria2helper-file-icon {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 44px;
    height: 50px;
    flex: 0 0 44px;
    border-radius: 7px;
    background: #e4edfb;
    color: #476ca7;
    font-size: 15px;
    font-weight: 750;
}
.aria2helper-bridge-dialog .aria2helper-file-details {
    min-width: 0;
    flex: 1;
}
.aria2helper-bridge-dialog .aria2helper-file-name {
    display: -webkit-box;
    overflow: hidden;
    overflow-wrap: anywhere;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
    line-height: 1.35;
    color: #252c36;
    font-size: 14px;
    font-weight: 650;
}
.aria2helper-bridge-dialog .aria2helper-file-meta {
    display: flex;
    gap: 6px 14px;
    flex-wrap: wrap;
    align-items: center;
    margin-top: 6px;
    color: #5e6977;
    font-size: 12px;
}
.aria2helper-bridge-dialog .aria2helper-file-meta span {
    overflow-wrap: anywhere;
}
.aria2helper-bridge-dialog select,
.aria2helper-bridge-dialog input[type="text"] {
    display: block;
    box-sizing: border-box;
    width: 100%;
    min-height: 38px;
    padding: 6px 10px;
    background: #fff;
    color: #222;
    border: 1px solid #aaa;
    border-radius: 5px;
    font-size: 14px;
}
.aria2helper-aria2-dir-dialog {
    width: min(560px, 100%);
}
.aria2helper-aria2-dir-dialog .aria2helper-aria2-dir-field {
    position: relative;
    display: flex;
    align-items: stretch;
    min-height: 44px;
    margin-top: 18px;
    border: 1px solid #aaa;
    border-radius: 7px;
    background: #fff;
}
.aria2helper-aria2-dir-dialog .aria2helper-aria2-dir-field:focus-within {
    border-color: #168579;
    box-shadow: 0 0 0 2px rgba(22, 133, 121, 0.12);
}
.aria2helper-aria2-dir-dialog .aria2helper-aria2-dir-field input[type="text"] {
    flex: 1 1 auto;
    width: 1px;
    min-width: 0;
    min-height: 44px;
    margin: 0;
    border: 0;
    outline: 0;
    box-shadow: none;
    border-radius: 7px 0 0 7px;
    background: transparent;
}
.aria2helper-aria2-dir-dialog .aria2helper-aria2-dir-history-toggle {
    display: flex;
    align-items: center;
    justify-content: center;
    flex: 0 0 46px;
    padding: 0;
    border: 0;
    border-left: 1px solid #ddd;
    border-radius: 0 7px 7px 0;
    background: #f8f8f8;
    color: #667080;
    cursor: pointer;
}
.aria2helper-aria2-dir-dialog .aria2helper-aria2-dir-history-toggle:hover:not(:disabled) {
    background: #eef5f3;
}
.aria2helper-aria2-dir-dialog .aria2helper-aria2-dir-history-toggle:disabled {
    opacity: 0.45;
    cursor: not-allowed;
}
.aria2helper-aria2-dir-dialog .aria2helper-aria2-dir-history-toggle svg {
    width: 20px;
    height: 20px;
    pointer-events: none;
}
.aria2helper-aria2-dir-dialog .aria2helper-aria2-dir-history-list {
    position: absolute;
    z-index: 10;
    top: calc(100% + 5px);
    left: 0;
    right: 0;
    max-height: min(240px, 32vh);
    overflow-y: auto;
    padding: 5px;
    border: 1px solid #ddd;
    border-radius: 7px;
    background: #fff;
    box-shadow: 0 8px 20px rgba(0,0,0,0.16);
}
.aria2helper-aria2-dir-dialog .aria2helper-aria2-dir-history-list[hidden] {
    display: none;
}
.aria2helper-aria2-dir-dialog .aria2helper-aria2-dir-history-list button {
    display: block;
    box-sizing: border-box;
    width: 100%;
    padding: 9px 10px;
    border: 0;
    border-radius: 5px;
    background: transparent;
    text-align: left;
    font: inherit;
    color: #222;
    overflow-wrap: anywhere;
    cursor: pointer;
}
.aria2helper-aria2-dir-dialog .aria2helper-aria2-dir-history-list button:hover,
.aria2helper-aria2-dir-dialog .aria2helper-aria2-dir-history-list button:focus-visible {
    background: #edf6f4;
}
.aria2helper-aria2-dir-dialog .aria2helper-aria2-dir-error {
    color: #bf3030;
    font-size: 12px;
    margin-top: 6px;
}
.aria2helper-aria2-dir-dialog .aria2helper-aria2-dir-error:empty {
    display: none;
}
.aria2helper-bridge-dialog select[hidden] {
    display: none;
}
.aria2helper-bridge-actions {
    display: flex;
    gap: 8px;
    justify-content: flex-end;
    margin-top: 18px;
}
.aria2helper-bridge-actions button {
    cursor: pointer;
    border: 1px solid #aaa;
    padding: 7px 14px;
    border-radius: 5px;
    background: #f4f4f4;
    color: #222;
}
.aria2helper-bridge-actions .aria2helper-bridge-confirm {
    background: #168579;
    border-color: #168579;
    color: #fff;
}
`;

class PikPakBridgeClient {
    getJSON(path) {
        return new Promise((resolve, reject) => {
            GM_xmlhttpRequest({
                method: 'GET', url: this.baseURL + path, timeout: 12000,
                onload: response => {
                    if(response.status !== 200) {
                        reject(new Error('Bridge 查询失败（HTTP ' + response.status + '）'));
                        return;
                    }
                    try { resolve(JSON.parse(response.responseText || '{}')); }
                    catch (_) { reject(new Error('Bridge 状态响应不是合法 JSON')); }
                },
                onerror: () => reject(new Error('无法连接 Bridge')),
                ontimeout: () => reject(new Error('Bridge 状态查询超时'))
            });
        });
    }
    listTasks() {
        return this.getJSON('/api/v1/tasks?limit=200').then(data =>
            Array.isArray(data.tasks) ? data.tasks : []);
    }
    getTask(id) { return this.getJSON('/api/v1/tasks/' + encodeURIComponent(id)); }
    getDownloads(id) {
        return this.getJSON('/api/v1/tasks/' + encodeURIComponent(id) + '/downloads')
            .then(data => Array.isArray(data.downloads) ? data.downloads : []);
    }

    constructor(baseURL) {
        const raw = String(baseURL || '').trim();
        if(!raw) throw new Error('请先在脚本设置中填写 pikpak-bridge 服务地址');
        let url;
        try {
            url = new URL(raw);
        } catch (_) {
            throw new Error('Bridge 地址格式无效，请填写 http:// 或 https:// 开头的地址');
        }
        if(!['http:', 'https:'].includes(url.protocol) || !url.hostname || url.username || url.password || url.search || url.hash) {
            throw new Error('Bridge 地址必须是无账号、查询参数或片段的 HTTP(S) 地址');
        }
        this.baseURL = url.href.replace(/\/+$/, '');
    }

    listTargets() {
        return new Promise((resolve, reject) => {
            GM_xmlhttpRequest({
                method: 'GET',
                url: this.baseURL + '/api/v1/targets',
                timeout: 15000,
                onload: response => {
                    let data;
                    try {
                        data = JSON.parse(response.responseText || '{}');
                    } catch (_) {
                        reject(new Error('Bridge 下载目标响应不是合法 JSON（HTTP ' + response.status + '）'));
                        return;
                    }
                    if(response.status !== 200) {
                        reject(new Error('获取 Bridge 下载目标失败（HTTP ' + response.status + '）：' +
                            (data.error || '请求失败')));
                        return;
                    }
                    if(!Array.isArray(data.targets)) {
                        reject(new Error('Bridge 返回的下载目标列表格式无效'));
                        return;
                    }
                    const targets = data.targets
                        .filter(target => target && typeof target.id === 'string' &&
                            target.id.trim() && target.enabled !== false)
                        .map(target => ({
                            id: target.id.trim(),
                            name: typeof target.name === 'string' && target.name.trim() ?
                                target.name.trim() : target.id.trim(),
                            dir: typeof target.dir === 'string' ? target.dir : '',
                            default: target.default === true
                        }));
                    if(!targets.length) {
                        reject(new Error('Bridge 没有可用的下载目标，请先在 Bridge 中配置并启用目标'));
                        return;
                    }
                    resolve(targets);
                },
                onerror: () => reject(new Error('无法连接 Bridge，请检查服务地址、网络及用户脚本跨域权限')),
                ontimeout: () => reject(new Error('获取 Bridge 下载目标超时，请确认服务可访问'))
            });
        });
    }

    retryTask(id) {
        return new Promise((resolve,reject)=>{
            GM_xmlhttpRequest({
                method:'POST',
                url:this.baseURL+'/api/v1/tasks/'+encodeURIComponent(id)+'/retry',
                timeout:15000,
                onload:response=>{
                    let body;
                    try {body=JSON.parse(response.responseText||'{}');}
                    catch (_) {reject(new Error('Bridge 重试响应不是合法 JSON'));return;}
                    if(response.status===200 && body.id) {
                        resolve({id:body.id,status:body.status,duplicate:false});
                    } else {
                        reject(new Error('重试失败（HTTP '+response.status+'）：'+(body.error||'请求失败')));
                    }
                },
                onerror:()=>reject(new Error('无法连接 Bridge')),
                ontimeout:()=>reject(new Error('Bridge 重试请求超时'))
            });
        });
    }

    addTask(magnet, target = '', force = false) {
        const body = {url: magnet};
        if(force) body.force = true;
        if(target) body.target = String(target).trim();
        return new Promise((resolve, reject) => {
            GM_xmlhttpRequest({
                method: 'POST',
                url: this.baseURL + '/api/v1/tasks',
                headers: {'Content-Type': 'application/json'},
                data: JSON.stringify(body),
                timeout: 15000,
                onload: response => {
                    let data;
                    try {
                        data = JSON.parse(response.responseText || '{}');
                    } catch (_) {
                        reject(new Error('Bridge 返回的响应不是合法 JSON（HTTP ' + response.status + '）'));
                        return;
                    }
                    if(response.status === 201 && typeof data.id === 'string' && data.id) {
                        resolve({id: data.id, status: data.status, duplicate: false});
                    } else if(response.status === 409 && typeof data.existing_task_id === 'string' && data.existing_task_id) {
                        resolve({id: data.existing_task_id, status: data.status, target: data.target_id, duplicate: true});
                    } else {
                        reject(new Error('Bridge HTTP ' + response.status + '：' + (data.error || '请求失败')));
                    }
                },
                onerror: () => reject(new Error('无法连接 Bridge，请检查服务地址、网络及用户脚本跨域权限')),
                ontimeout: () => reject(new Error('连接 Bridge 超时，请确认服务可访问'))
            });
        });
    }
}

// E-Hentai torrent URL identifiers are not guaranteed to be BitTorrent info hashes.
// Extract the exact bencoded "info" bytes from the actual torrent and SHA-1 those bytes.
function torrentInfoFromBytes(arrayBuffer) {
    const bytes = new Uint8Array(arrayBuffer);
    if(bytes.length < 8 || bytes.length > 16 * 1024 * 1024) {
        throw new Error('种子文件为空或超过 16 MiB，拒绝解析');
    }

    let pos = 0;
    let nodes = 0;
    let foundPieces = false;
    let infoRange = null;
    let announce = '';

    const isDigit = value => value >= 48 && value <= 57;
    const isKey = (slice, text) => {
        if(slice.end - slice.start !== text.length) return false;
        for(let i = 0; i < text.length; i++) {
            if(bytes[slice.start + i] !== text.charCodeAt(i)) return false;
        }
        return true;
    };
    const fail = () => { throw new Error('下载内容不是有效的 BitTorrent 种子文件'); };

    function readString() {
        if(pos >= bytes.length || !isDigit(bytes[pos])) fail();
        let size = 0;
        let digits = 0;
        while(pos < bytes.length && isDigit(bytes[pos])) {
            size = size * 10 + bytes[pos++] - 48;
            if(++digits > 10 || size > bytes.length) fail();
        }
        if(bytes[pos++] !== 58 || size > bytes.length - pos) fail();
        const result = {start: pos, end: pos + size};
        pos += size;
        return result;
    }

    function scanValue(depth, insideInfo = false) {
        if(depth > 64 || ++nodes > 200000 || pos >= bytes.length) fail();
        const prefix = bytes[pos];
        if(isDigit(prefix)) return {type: 'string', ...readString()};
        if(prefix === 105) { // i<number>e
            const start = ++pos;
            while(pos < bytes.length && bytes[pos] !== 101) {
                if(!isDigit(bytes[pos]) && !(pos === start && bytes[pos] === 45)) fail();
                pos++;
            }
            if(pos === start || pos >= bytes.length) fail();
            pos++;
            return {type: 'integer'};
        }
        if(prefix !== 100 && prefix !== 108) fail(); // d / l
        pos++;
        if(prefix === 100) {
            while(pos < bytes.length && bytes[pos] !== 101) {
                const key = readString();
                const isPieces = insideInfo && isKey(key, 'pieces');
                const value = scanValue(depth + 1);
                if(isPieces && value.type === 'string' &&
                    value.end - value.start > 0 &&
                    (value.end - value.start) % 20 === 0) {
                    foundPieces = true;
                }
            }
        } else {
            while(pos < bytes.length && bytes[pos] !== 101) scanValue(depth + 1);
        }
        if(pos >= bytes.length) fail();
        pos++;
        return {type: prefix === 100 ? 'dict' : 'list'};
    }

    if(bytes[pos++] !== 100) fail();
    while(pos < bytes.length && bytes[pos] !== 101) {
        const key = readString();
        const start = pos;
        const isInfo = isKey(key, 'info');
        const value = scanValue(1, isInfo);
        if(isInfo) {
            if(infoRange || value.type !== 'dict') fail();
            infoRange = {start, end: pos};
        } else if(isKey(key, 'announce') && value.type === 'string') {
            announce = new TextDecoder('utf-8').decode(bytes.subarray(value.start, value.end));
        }
    }
    if(bytes[pos++] !== 101 || pos !== bytes.length || !infoRange || !foundPieces) {
        throw new Error('种子缺少完整的 BT v1 info/pieces 元数据（暂不支持纯 BT v2 种子）');
    }
    return {infoBytes: bytes.subarray(infoRange.start, infoRange.end), announce};
}

function fetchTorrentBytes(link) {
    let parsed;
    try {
        parsed = new URL(link, window.location.href);
    } catch (_) {
        throw new Error('种子下载地址无效');
    }
    const host = parsed.hostname.toLowerCase();
    const permitted = ['e-hentai.org', 'exhentai.org', 'ehtracker.org'];
    if(!['http:', 'https:'].includes(parsed.protocol) ||
       parsed.username || parsed.password ||
       !permitted.some(domain => host === domain || host.endsWith('.' + domain))) {
        throw new Error('不允许读取非 E-Hentai/ExHentai/EHTracker 的种子链接');
    }

    return new Promise((resolve, reject) => {
        GM_xmlhttpRequest({
            method: 'GET',
            url: parsed.href,
            responseType: 'arraybuffer',
            timeout: 20000,
            onload: response => {
                if(response.status !== 200) {
                    reject(new Error('下载种子文件失败（HTTP ' + response.status + '），请确认种子链接可以访问'));
                    return;
                }
                const body = response.response;
                if(!body || typeof body.byteLength !== 'number' ||
                   body.byteLength > 16 * 1024 * 1024) {
                    reject(new Error('种子文件响应无效或体积超过 16 MiB'));
                    return;
                }
                resolve(body);
            },
            onerror: () => reject(new Error('下载种子文件失败，请检查登录状态或跨域访问权限')),
            ontimeout: () => reject(new Error('下载种子文件超时'))
        });
    });
}

async function bridgeMagnetFromTorrentLink(link) {
    const source = String(link || '').trim();
    if(/^magnet:\?/i.test(source)) {
        if(!/urn:btih:[a-f0-9]{40}(?![a-z0-9])/i.test(source)) {
            throw new Error('磁链缺少有效的 40 位 BTIH');
        }
        return source;
    }
    const downloaded = await fetchTorrentBytes(source);
    const {infoBytes, announce} = torrentInfoFromBytes(downloaded);
    if(!globalThis.crypto || !globalThis.crypto.subtle) {
        throw new Error('当前页面不支持 Web Crypto API，无法计算 BTIH');
    }
    const digest = await globalThis.crypto.subtle.digest('SHA-1', infoBytes);
    const hash = Array.from(new Uint8Array(digest), byte =>
        byte.toString(16).padStart(2, '0')).join('').toUpperCase();
    let magnet = 'magnet:?xt=urn:btih:' + hash;
    if(/^https?:\/\/\S+|^udp:\/\/\S+/i.test(announce)) {
        magnet += '&tr=' + encodeURIComponent(announce);
    }
    return magnet;
}

// Remember the last successful target separately for each Bridge service.
function bridgeLastTargetStorageKey(baseURL) {
    return 'PIKPAK_BRIDGE_LAST_TARGET:' + baseURL;
}

function preferredBridgeTarget(targets, baseURL) {
    const lastID = GM_getValue(bridgeLastTargetStorageKey(baseURL), '');
    return targets.find(target => target.id === lastID) ||
        targets.find(target => target.default) ||
        targets[0];
}

// Each aria2 RPC endpoint keeps its own bounded, recent-first directory history.
// The history is stored by the userscript manager, not by Bridge or the web page.
function aria2DirectoryHistoryKey(rpc) {
    return 'ARIA2_DIR_HISTORY:' + String(rpc || '').trim();
}

function aria2DirectoryHistory(rpc) {
    const saved = GM_getValue(aria2DirectoryHistoryKey(rpc), []);
    if(!Array.isArray(saved)) return [];
    return [...new Set(saved.filter(dir => typeof dir === 'string')
        .map(dir => dir.trim()).filter(Boolean))].slice(0, 10);
}

function rememberAria2Directory(rpc, directory) {
    const dir = String(directory || '').trim();
    if(!dir) return;
    const recent = aria2DirectoryHistory(rpc).filter(entry => entry !== dir);
    GM_setValue(aria2DirectoryHistoryKey(rpc), [dir, ...recent].slice(0, 10));
}

// Only call this once the aria2 RPC confirms a task was accepted.
async function submitToAria2(uri, directory) {
    const id = await ariaClient.addUri(uri, directory);
    if(!id) throw new Error('aria2 未返回任务 ID，未保存此次目录');
    rememberAria2Directory(ariaClient.rpc, directory);
    return id;
}

// Display only metadata that is actually available before a task is created.
function galleryFileInfo(root = document) {
    const name = root.querySelector('#gn')?.textContent?.trim() ||
        root.querySelector('#gj')?.textContent?.trim() || '';
    const rows = Array.from(root.querySelectorAll('#gdd tr'));
    let size = '';
    let pages = '';
    for(const row of rows) {
        const label = row.querySelector('.gdt1')?.textContent?.trim() || '';
        const value = row.querySelector('.gdt2')?.textContent?.trim() || '';
        if(/^File Size:?\s*$/i.test(label) && value) size = value;
        if(/^Length:?\s*$/i.test(label) && /^\d[\d,]*\s+pages?\b/i.test(value)) pages = value;
    }
    return {name, size, pages};
}

function torrentFileInfo(item, gid) {
    if(!item) return null;
    // Gallery detail metadata belongs only to the gallery currently being viewed.
    const currentGallery = IS_GALLERY_DETAIL_PAGE && Number(gid) === Number(GID) ?
        galleryFileInfo() : {};
    return {
        name: String(item.name || '').trim(),
        size: String(item.size || '').trim(),
        pages: currentGallery.pages || '',
        kind: 'torrent'
    };
}

function torrentPageFileInfo(table) {
    const link = table?.querySelector('a');
    if(!link) return null;
    const text = table.textContent || '';
    const size = text.match(/\b(?:Size|File Size):\s*([\d,.]+\s*[KMGT]?i?B)\b/i);
    return {
        name: (link.textContent || '').trim(),
        size: size ? size[1].trim() : '',
        kind: 'torrent'
    };
}

// Archive one-click buttons on gallery lists may link through a cover image with
// no text. Read the title from the gallery's own title element, not that cover link.
function galleryListArchiveFileInfo(row) {
    if(!row) return {name: '', size: '', pages: '', kind: 'archive'};

    // E-Hentai uses different title wrappers in extended, compact, minimal
    // and thumbnail layouts. Restrict all lookups to this one gallery item.
    const titleSelectors = ['.glink', '.gl4t.glname', '.glname a', '.glname'];
    let name = '';
    for(const selector of titleSelectors) {
        const titleNode = row.querySelector(selector);
        name = titleNode?.textContent?.trim() || '';
        if(name) break;
    }

    const text = row.textContent || '';
    const pageMatch = text.match(/(?:^|\D)(\d[\d,]*)\s+pages?\b/i);
    // A size without an explicit label may belong to something else on a list.
    // Do not present it as the archive's size.
    const sizeMatch = text.match(/\bFile Size\s*:\s*([\d,.]+\s*[KMGT]?i?B)\b/i);

    return {
        name,
        size: sizeMatch ? sizeMatch[1].trim() : '',
        pages: pageMatch ? pageMatch[1] + ' pages' : '',
        kind: 'archive'
    };
}

function archiveFileInfo(title = '') {
    const gallery = IS_GALLERY_DETAIL_PAGE ? galleryFileInfo() : {};
    return {
        name: String(title || gallery.name || '').trim(),
        size: gallery.size || '',
        pages: gallery.pages || '',
        kind: 'archive'
    };
}

function makeDownloadFileInfo(info) {
    if(!info) return null;
    const name = String(info.name || '').trim();
    const size = String(info.size || '').trim();
    const pages = String(info.pages || '').trim();
    if(!name && !size && !pages) return null;
    const card = document.createElement('div');
    card.className = 'aria2helper-file-info';
    card.setAttribute('role', 'group');
    card.setAttribute('aria-label', '待下载文件信息');

    const icon = document.createElement('div');
    icon.className = 'aria2helper-file-icon';
    const extension = name.match(/\.([a-z0-9]{2,6})$/i)?.[1]?.toUpperCase();
    // Torrent info names may describe folders or multi-file downloads; do not assert a ZIP.
    icon.textContent = info.kind === 'archive' ? 'ZIP' :
        (extension && ['ZIP', 'RAR', '7Z', 'CBZ'].includes(extension) ? extension : 'BT');
    card.appendChild(icon);

    const details = document.createElement('div');
    details.className = 'aria2helper-file-details';
    if(name) {
        const filename = document.createElement('div');
        filename.className = 'aria2helper-file-name';
        filename.textContent = name;
        filename.title = name;
        details.appendChild(filename);
    }
    const meta = document.createElement('div');
    meta.className = 'aria2helper-file-meta';
    const addMeta = value => {
        if(!value) return;
        const span = document.createElement('span');
        span.textContent = value;
        meta.appendChild(span);
    };
    addMeta(size ? (info.kind === 'archive' ? '画廊参考大小：' : '大小：') + size : '');
    addMeta(info.kind === 'archive' ? 'ZIP 存档' :
        (extension && ['ZIP', 'RAR', '7Z', 'CBZ'].includes(extension) ?
            extension + '（按名称推断）' : 'BT 种子'));
    addMeta(pages ? '页数：' + pages : '');
    details.appendChild(meta);
    card.appendChild(details);
    return card;
}

function chooseAria2Directory(rpc, fileInfo = null) {
    const recent = aria2DirectoryHistory(rpc);
    return new Promise(resolve => {
        const previousFocus = document.activeElement;
        const overlay = document.createElement('div');
        overlay.className = 'aria2helper-bridge-overlay';
        const dialog = document.createElement('div');
        dialog.className = 'aria2helper-bridge-dialog aria2helper-aria2-dir-dialog';
        dialog.setAttribute('role', 'dialog');
        dialog.setAttribute('aria-modal', 'true');
        dialog.setAttribute('aria-label', '选择 aria2 保存位置');

        const title = document.createElement('h3');
        title.textContent = '选择 aria2 保存位置';
        const description = document.createElement('p');
        description.textContent = '请输入 aria2 服务器上的保存目录：';

        // One combined row: editable directory input + icon-only history button.
        const field = document.createElement('div');
        field.className = 'aria2helper-aria2-dir-field';
        const directory = document.createElement('input');
        directory.type = 'text';
        directory.setAttribute('aria-label', 'aria2 保存目录');
        directory.setAttribute('placeholder', '例如 /downloads');
        directory.value = recent[0] || '';
        field.appendChild(directory);

        const historyButton = document.createElement('button');
        historyButton.type = 'button';
        historyButton.className = 'aria2helper-aria2-dir-history-toggle';
        historyButton.setAttribute('aria-label', '选择历史保存目录');
        historyButton.setAttribute('aria-haspopup', 'menu');
        historyButton.setAttribute('aria-expanded', 'false');
        historyButton.title = recent.length ? '选择历史保存目录' : '暂无历史保存目录';
        historyButton.disabled = recent.length === 0;
        historyButton.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"></circle><path d="M12 7v5l3 2"></path></svg>';
        field.appendChild(historyButton);

        const historyList = document.createElement('div');
        historyList.className = 'aria2helper-aria2-dir-history-list';
        historyList.hidden = true;
        historyList.setAttribute('role', 'menu');
        historyList.setAttribute('aria-label', '历史保存目录');
        const error = document.createElement('div');
        error.className = 'aria2helper-aria2-dir-error';
        error.setAttribute('role', 'status');

        const setHistoryOpen = open => {
            historyList.hidden = !open;
            historyButton.setAttribute('aria-expanded', String(open));
            if(open && historyList.firstElementChild) historyList.firstElementChild.focus();
        };
        for(const path of recent) {
            const item = document.createElement('button');
            item.type = 'button';
            item.setAttribute('role', 'menuitem');
            item.textContent = path;
            item.onclick = () => {
                directory.value = path;
                error.textContent = '';
                setHistoryOpen(false);
                directory.focus();
            };
            historyList.appendChild(item);
        }
        historyButton.onclick = () => {
            if(historyButton.disabled) return;
            setHistoryOpen(historyList.hidden);
        };
        directory.oninput = () => {
            error.textContent = '';
            if(!historyList.hidden) setHistoryOpen(false);
        };
        field.appendChild(historyList);

        const actions = document.createElement('div');
        actions.className = 'aria2helper-bridge-actions';
        const cancel = document.createElement('button');
        cancel.type = 'button';
        cancel.textContent = '取消';
        const confirm = document.createElement('button');
        confirm.type = 'button';
        confirm.className = 'aria2helper-bridge-confirm';
        confirm.textContent = '发送到 aria2';

        let closed = false;
        const finish = value => {
            if(closed) return;
            closed = true;
            document.removeEventListener('keydown', onKeydown, true);
            document.removeEventListener('pointerdown', onOutsideHistory, true);
            overlay.remove();
            if(previousFocus && typeof previousFocus.focus === 'function') previousFocus.focus();
            resolve(value);
        };
        const submit = () => {
            const dir = directory.value.trim();
            if(!dir) {
                error.textContent = '请填写保存目录';
                directory.focus();
                return;
            }
            finish(dir);
        };
        const onKeydown = event => {
            if(event.key === 'Escape') {
                event.preventDefault();
                if(!historyList.hidden) {
                    setHistoryOpen(false);
                    historyButton.focus();
                } else {
                    finish(null);
                }
            } else if(event.key === 'Enter' && (event.target === directory || event.target === confirm)) {
                event.preventDefault();
                submit();
            }
        };
        const onOutsideHistory = event => {
            if(!historyList.hidden && event.target !== historyButton && !historyList.contains(event.target)) {
                setHistoryOpen(false);
            }
        };
        cancel.onclick = () => finish(null);
        confirm.onclick = submit;
        overlay.onclick = event => {
            if(event.target === overlay) finish(null);
        };
        actions.appendChild(cancel);
        actions.appendChild(confirm);
        dialog.appendChild(title);
        dialog.appendChild(description);
        const fileCard = makeDownloadFileInfo(fileInfo);
        if(fileCard) dialog.appendChild(fileCard);
        dialog.appendChild(field);
        dialog.appendChild(error);
        dialog.appendChild(actions);
        overlay.appendChild(dialog);
        document.addEventListener('keydown', onKeydown, true);
        document.addEventListener('pointerdown', onOutsideHistory, true);
        document.body.appendChild(overlay);
        directory.focus();
    });
}

// Render immediately, even when the Bridge target lookup is still pending.
// A single enabled target continues to be selected automatically.
function chooseBridgeTarget(targetsOrPromise, baseURL, fileInfo = null) {
    return new Promise((resolve, reject) => {
        const previousFocus = document.activeElement;
        const overlay = document.createElement('div');
        overlay.className = 'aria2helper-bridge-overlay';
        const dialog = document.createElement('div');
        dialog.className = 'aria2helper-bridge-dialog';
        dialog.setAttribute('role', 'dialog');
        dialog.setAttribute('aria-modal', 'true');
        dialog.setAttribute('aria-label', '选择 PikPak Bridge 下载目标');

        const title = document.createElement('h3');
        title.textContent = '选择下载目标';
        const explanation = document.createElement('p');
        explanation.textContent = '正在获取 PikPak Bridge 下载目标…';
        explanation.setAttribute('role', 'status');
        explanation.setAttribute('aria-live', 'polite');
        const select = document.createElement('select');
        select.setAttribute('aria-label', '下载目标');
        select.hidden = true;
        select.disabled = true;

        const actions = document.createElement('div');
        actions.className = 'aria2helper-bridge-actions';
        const cancel = document.createElement('button');
        cancel.type = 'button';
        cancel.textContent = '取消';
        const confirm = document.createElement('button');
        confirm.type = 'button';
        confirm.textContent = '发送到 PikPak';
        confirm.className = 'aria2helper-bridge-confirm';
        confirm.disabled = true;

        let closed = false;
        const finish = (value, error = null) => {
            if(closed) return;
            closed = true;
            document.removeEventListener('keydown', onKeydown, true);
            overlay.remove();
            if(previousFocus && typeof previousFocus.focus === 'function') previousFocus.focus();
            if(error) reject(error);
            else resolve(value);
        };
        const onKeydown = event => {
            if(event.key === 'Escape') {
                event.preventDefault();
                finish(null);
            }
        };
        const showTargets = targets => {
            if(closed) return; // Ignore late responses after cancel.
            if(!Array.isArray(targets) || !targets.length) {
                finish(null, new Error('Bridge 没有可用的下载目标'));
                return;
            }
            if(targets.length === 1) {
                finish(targets[0]);
                return;
            }
            for(const target of targets) {
                const option = document.createElement('option');
                option.value = target.id;
                option.textContent = target.name + (target.dir ? ' · ' + target.dir : '');
                select.appendChild(option);
            }
            select.value = preferredBridgeTarget(targets, baseURL).id;
            select.hidden = false;
            select.disabled = false;
            confirm.disabled = false;
            explanation.textContent = '以下目标实时获取自 PikPak Bridge：';
            select.focus();
            confirm.onclick = () => finish(targets.find(target => target.id === select.value) || null);
        };

        cancel.onclick = () => finish(null);
        overlay.onclick = event => {
            if(event.target === overlay) finish(null);
        };
        actions.appendChild(cancel);
        actions.appendChild(confirm);
        dialog.appendChild(title);
        dialog.appendChild(explanation);
        const fileCard = makeDownloadFileInfo(fileInfo);
        if(fileCard) dialog.appendChild(fileCard);
        dialog.appendChild(select);
        dialog.appendChild(actions);
        overlay.appendChild(dialog);
        document.addEventListener('keydown', onKeydown, true);
        document.body.appendChild(overlay);
        cancel.focus();

        // Direct callers may still supply a resolved target array.
        if(Array.isArray(targetsOrPromise)) showTargets(targetsOrPromise);
        else Promise.resolve(targetsOrPromise).then(showTargets, error => {
            if(!closed) finish(null, error);
        });
    });
}

// Keep magnet copying and PikPak submission on the same byte-accurate torrent parser.
async function copyTorrentMagnetToClipboard(torrentLink, button) {
    if(button.dataset.copyBusy === '1') return;
    const originalText = button.textContent;
    const originalTitle = button.title;
    button.dataset.copyBusy = '1';
    button.textContent = '…';
    try {
        const magnet = await bridgeMagnetFromTorrentLink(torrentLink);
        GM_setClipboard(magnet, 'text');
        button.textContent = '✔';
        button.title = '磁链已复制到剪贴板';
    } catch (error) {
        console.error('[EhPikPakAria2] 复制磁链失败：', error);
        button.textContent = '✕';
        alert('复制磁链失败：' + (error.message || String(error)));
    } finally {
        button.dataset.copyBusy = '0';
        setTimeout(() => {
            button.textContent = originalText;
            button.title = originalTitle;
        }, 2000);
    }
}

function setBridgeButtonText(button, text) {
    if(button.tagName === 'INPUT') button.value = text;
    else button.textContent = text;
}

async function sendTorrentToBridge(torrentLink, button, fileInfo = null) {
    if(button.dataset.bridgeBusy === '1') return;
    const originalText = button.tagName === 'INPUT' ? button.value : button.textContent;
    button.dataset.bridgeBusy = '1';
    if('disabled' in button) button.disabled = true;
    setBridgeButtonText(button, '获取目标…');
    try {
        const client = new PikPakBridgeClient(gmc.get('BRIDGE_URL'));
        const selected = await chooseBridgeTarget(client.listTargets(), client.baseURL, fileInfo);
        if(!selected) {
            setBridgeButtonText(button, originalText);
            return;
        }
        setBridgeButtonText(button, '解析种子…');
        const magnet = await bridgeMagnetFromTorrentLink(torrentLink);
        setBridgeButtonText(button, '提交中…');
        let task = await client.addTask(magnet, selected.id);
        if(task.duplicate && ['COMPLETED','CANCELLED'].includes(task.status)) {
            const proceed = window.confirm('这个种子之前已经下载过（' +
                (task.status === 'COMPLETED' ? '已完成' : '已取消') +
                '）。\n确定重新下载吗？Bridge 会直接通过 aria2 下载并覆盖同名 NAS 文件；下载失败可能导致原文件不完整。');
            if(!proceed) {
                setBridgeButtonText(button, '已取消');
                return;
            }
            setBridgeButtonText(button, '重新提交…');
            task = await client.addTask(magnet, selected.id, true);
        } else if(task.duplicate && BRIDGE_ACTIVE.has(task.status)) {
            setBridgeButtonText(button, '进行中');
            button.title = '该种子已有正在运行的任务：' + task.id;
            bridgeProgressMonitor.remember(torrentLink, task.id, button.dataset.gid || GID, fileInfo);
            return;
        } else if(task.duplicate && BRIDGE_FAILED.has(task.status)) {
            const proceed = window.confirm('该种子的旧任务失败。确定重试原任务吗？系统会优先复用原有云端资源。');
            if(!proceed) {setBridgeButtonText(button, '已取消');return;}
            task = await client.retryTask(task.id);
        }
        if(!task.duplicate) {
            GM_setValue(bridgeLastTargetStorageKey(client.baseURL), selected.id);
        }
        bridgeProgressMonitor.remember(torrentLink, task.id, button.dataset.gid || GID, fileInfo);
        setBridgeButtonText(button, task.duplicate ? '已存在' : '已提交');
        button.title = (task.duplicate ? 'Bridge 中已有该任务' : 'Bridge 任务已创建') +
            '：' + task.id + (task.target ? '（目标：' + task.target + '）' : '');
    } catch (error) {
        console.error('[EhAria2 + PikPak Bridge]', error);
        setBridgeButtonText(button, '失败');
        alert('发送到 PikPak Bridge 失败：' + (error.message || String(error)));
    } finally {
        button.dataset.bridgeBusy = '0';
        if('disabled' in button) button.disabled = false;
        setTimeout(() => setBridgeButtonText(button, originalText), 3000);
    }
}

class SendTaskButton {
    constructor(gid, link, fileInfo = null) {
        this.element = document.createElement("div");;
        this.link = link;
        this.gid = gid;
        this.fileInfo = fileInfo;

        this.element.className = "aria2helper-box";
        this.loading = document.createElement("div");
        this.loading.className = "aria2helper-loading";
        this.loading.innerHTML = SVG_LOADING_ICON;

        this.message = document.createElement("div");
        this.message.className = "aria2helper-message";

        this.button = document.createElement("input");
        this.button.type = "button";
        this.button.value = "发送到Aria2";
        this.button.className = 'stdbtn aria2helper-button';
        this.button.onclick = () => this.buttonClick();
        this.element.appendChild(this.button);
        this.bridgeButton = document.createElement('input');
        this.bridgeButton.type = 'button';
        this.bridgeButton.value = '发送到 PikPak';
        this.bridgeButton.className = 'stdbtn aria2helper-bridge-button';
        this.bridgeButton.title = '将此种子的磁链交给 pikpak-bridge 处理';
        this.bridgeButton.dataset.gid = String(gid);
        this.bridgeButton.onclick = () => sendTorrentToBridge(this.link, this.bridgeButton, this.fileInfo);
        this.element.appendChild(this.bridgeButton);
        this.element.appendChild(this.loading);
        this.element.appendChild(this.message);
        this.message.onclick = () => this.show(this.button);
        this.show(this.button);
    }

    show(node) {
        this.loading.style.display = 'none';
        this.message.style.display = 'none';
        this.button.style.display = 'none';
        node.style.display = '';
    }

    showMessage(msg) {
        this.message.textContent = msg;
        this.show(this.message);
    }
    showLoading() {
        this.show(this.loading);
    }

    async buttonClick() {
        if(this.aria2Busy) return;
        if(!gmc.get('ARIA2_RPC')) {
            this.showMessage('请先配置 Aria2 RPC');
            return;
        }
        this.aria2Busy = true;
        try {
            const dir = await chooseAria2Directory(ariaClient.rpc, this.fileInfo);
            if(dir === null) return;
            this.showLoading();
            const id = await submitToAria2(getTorrentLink(this.link), dir);
            Tool.setTaskId(this.gid, id);
            this.showMessage("成功");
        } catch (error) {
            console.error(error);
            if(typeof error === 'string') this.showMessage(error || "请求失败");
            else if(error.status) this.showMessage("请求失败 HTTP" + error.status);
            else this.showMessage(error.message || "请求失败");
        } finally {
            this.aria2Busy = false;
        }
    }
}


// Associate each Bridge task with its individual torrent URL, not just a gallery ID.
const BRIDGE_ACTIVE = new Set([
    'QUEUED', 'WAITING_PIKPAK_ACCOUNT', 'PIKPAK_SUBMITTING', 'PIKPAK_RUNNING',
    'PIKPAK_COMPLETE', 'RESOLVING_FILES', 'WAITING_ARIA2', 'ARIA2_DOWNLOADING',
    'VERIFYING', 'READY_TO_CLEANUP', 'PIKPAK_DELETING', 'CANCELLING'
]);
const BRIDGE_FAILED = new Set(['PIKPAK_FAILED', 'ARIA2_FAILED', 'VERIFY_FAILED', 'CLEANUP_FAILED']);
const BRIDGE_LABELS = {
    QUEUED:'排队中', WAITING_PIKPAK_ACCOUNT:'等待 PikPak 账号',
    PIKPAK_SUBMITTING:'提交 PikPak 中', PIKPAK_RUNNING:'PikPak 云下载中',
    PIKPAK_COMPLETE:'PikPak 离线完成', RESOLVING_FILES:'解析文件中',
    WAITING_ARIA2:'等待 Aria2', ARIA2_DOWNLOADING:'Aria2 下载中',
    VERIFYING:'校验中', READY_TO_CLEANUP:'等待清理',
    PIKPAK_DELETING:'清理 PikPak 中', COMPLETED:'已完成',
    CANCELLED:'已取消', CANCELLING:'正在取消',
    PIKPAK_FAILED:'PikPak 失败', ARIA2_FAILED:'Aria2 失败',
    VERIFY_FAILED:'校验失败', CLEANUP_FAILED:'清理失败'
};
function bridgeProgressOf(task, downloads = []) {
    const status = String(task?.status || '');
    let percent = null;
    if(['PIKPAK_SUBMITTING', 'PIKPAK_RUNNING', 'PIKPAK_COMPLETE', 'RESOLVING_FILES'].includes(status)) {
        const value = Number(task.pikpak_progress);
        if(Number.isFinite(value) && value >= 0) percent = Math.min(100, value);
    } else if(['ARIA2_DOWNLOADING', 'VERIFYING', 'READY_TO_CLEANUP', 'PIKPAK_DELETING'].includes(status) &&
        downloads.length) {
        const total = downloads.reduce((sum, file) =>
            sum + Math.max(0, Number(file.total_length || file.expected_size) || 0), 0);
        const completed = downloads.reduce((sum, file) =>
            sum + Math.max(0, Number(file.completed_length) || 0), 0);
        if(total > 0) percent = Math.min(100, completed / total * 100);
    }
    if(status === 'COMPLETED') percent = 100;
    return {
        label: (BRIDGE_LABELS[status] || '未知状态') +
            (percent === null ? '' : ' ' + percent.toFixed(0) + '%'),
        percent, error: String(task?.error || ''),
        state: BRIDGE_FAILED.has(status) ? 'failed' :
            status === 'COMPLETED' ? 'completed' : 'active'
    };
}
function bridgeTorrentKey(link) {
    const raw = String(link || '').trim();
    try {
        const url = new URL(raw, window.location.href);
        return url.protocol === 'magnet:' ? raw : url.href.split('#')[0];
    } catch (_) { return raw; }
}
class BridgeProgressMonitor {
    constructor() {
        this.service = '';
        this.records = [];
        this.views = [];
        this.tasks = new Map();
        this.downloads = new Map();
        this.timer = 0;
        this.polling = false;
        this.refreshAfterPoll = false;
        this.stopped = false;
    }
    prepare() {
        let service = '';
        try {
            if(gmc.get('BRIDGE_URL')) service = new PikPakBridgeClient(gmc.get('BRIDGE_URL')).baseURL;
        } catch (_) { /* Invalid configuration: leave progress hidden. */ }
        if(service === this.service) return service;
        this.stop();
        this.stopped = false;
        this.service = service;
        this.tasks.clear();
        this.downloads.clear();
        const saved = service ? GM_getValue(this.storageKey(), []) : [];
        this.records = Array.isArray(saved) ? saved.filter(r =>
            r && typeof r.link === 'string' && typeof r.id === 'string' &&
            r.link && r.id && /^\d+$/.test(String(r.gid || ''))).slice(0, 120) : [];
        return service;
    }
    storageKey() { return 'PIKPAK_BRIDGE_TRACKED_TASKS:' + this.service; }
    stop() {
        this.stopped = true;
        if(this.timer) clearTimeout(this.timer);
        this.timer = 0;
    }
    remember(link, id, gid, fileInfo = null) {
        if(!this.prepare() || typeof id !== 'string' || !id ||
            !/^\d+$/.test(String(gid))) return;
        const key = bridgeTorrentKey(link);
        this.records = [{
            link: key, id, gid: String(gid),
            name: String(fileInfo?.name || '').slice(0, 200), at: Date.now()
        }, ...this.records.filter(r => r.link !== key)].slice(0, 120);
        GM_setValue(this.storageKey(), this.records);
        this.render();
        this.schedule(0);
    }
    watchTorrent(link) { return this.watch('torrent', bridgeTorrentKey(link)); }
    watchGallery(gid) { return this.watch('gallery', String(gid)); }
    watch(kind, key) {
        const element = document.createElement('div');
        element.className = 'aria2helper-bridge-progress';
        element.setAttribute('role', 'status');
        element.setAttribute('aria-live', 'polite');
        const label = document.createElement('span');
        label.className = 'aria2helper-bridge-progress-label';
        const track = document.createElement('div');
        track.className = 'aria2helper-bridge-progress-track';
        const fill = document.createElement('span');
        fill.className = 'aria2helper-bridge-progress-fill';
        track.appendChild(fill);
        element.appendChild(label);
        element.appendChild(track);
        this.views.push({kind, key, element, label, track, fill});
        this.prepare();
        this.render();
        this.schedule(0);
        return element;
    }
    visibleRecords() {
        // Newly created widgets are briefly disconnected until appended to a page.
        // Only prune views which have actually been mounted and then removed.
        this.views = this.views.filter(v => {
            if(v.element.isConnected === true) v.mounted = true;
            return !v.mounted || v.element.isConnected !== false;
        });
        const links = new Set(this.views.filter(v => v.kind === 'torrent').map(v => v.key));
        const galleries = new Set(this.views.filter(v => v.kind === 'gallery').map(v => v.key));
        return this.records.filter(r => links.has(r.link) || galleries.has(r.gid));
    }
    render() {
        for(const view of this.views) {
            const entries = this.records.filter(r =>
                view.kind === 'torrent' ? r.link === view.key : r.gid === view.key);
            if(!entries.length || !this.service) {
                view.element.style.display = 'none';
                continue;
            }
            const running = entries.find(r => BRIDGE_ACTIVE.has(this.tasks.get(r.id)?.status));
            const selected = running || entries[0];
            const task = this.tasks.get(selected.id);
            const progress = task ? bridgeProgressOf(task, this.downloads.get(selected.id) || []) : null;
            const isCompleted = progress?.state === 'completed';
            // Gallery cells can be narrow, so show errors there in the tooltip instead of the inline label.
            const errorHint = progress?.error && view.kind === 'torrent' ?
                ' · ' + progress.error.slice(0, 48) + (progress.error.length > 48 ? '…' : '') : '';
            view.label.textContent = isCompleted ?
                '✓ PikPak 已完成' + (view.kind === 'gallery' && entries.length > 1 ?
                    ' (' + entries.length + '项)' : '') :
                (view.kind === 'gallery' ? 'PikPak (' + entries.length + '项) · ' : 'PikPak · ') +
                    (progress ? progress.label : '查询任务中…') + errorHint;
            view.element.dataset.state = progress?.state || 'active';
            view.element.style.display = isCompleted ? 'inline-block' : 'block';
            view.element.title = progress?.error ?
                progress.label + '：' + progress.error : view.label.textContent;
            view.track.style.display = isCompleted || progress?.percent == null ? 'none' : 'block';
            view.fill.style.width = progress?.percent == null ? '0%' : progress.percent.toFixed(2) + '%';
        }
    }
    schedule(delay = 5000) {
        if(this.stopped || !this.service || !this.visibleRecords().length) return;
        if(this.polling) {
            this.refreshAfterPoll = this.refreshAfterPoll || delay === 0;
            return;
        }
        if(this.timer) {
            if(delay !== 0) return;
            clearTimeout(this.timer);
        }
        this.timer = setTimeout(() => {
            this.timer = 0;
            this.poll();
        }, delay);
    }
    async poll() {
        if(this.polling || !this.prepare()) return;
        const visible = this.visibleRecords();
        if(!visible.length) return;
        this.polling = true;
        let interval = 0;
        try {
            const client = new PikPakBridgeClient(this.service);
            const list = await client.listTasks();
            const byID = new Map(list.filter(t => t && t.id).map(t => [t.id, t]));
            const ids = [...new Set(visible.map(r => r.id))];
            const missing = ids.filter(id => !byID.has(id)).slice(0, 8);
            for(let i = 0; i < missing.length; i += 4) {
                await Promise.all(missing.slice(i, i + 4).map(async id => {
                    try { byID.set(id, await client.getTask(id)); }
                    catch (_) { /* Preserve the last known state on transient failures. */ }
                }));
            }
            for(const id of ids) if(byID.has(id)) this.tasks.set(id, byID.get(id));
            const transferring = ids.filter(id =>
                ['ARIA2_DOWNLOADING', 'VERIFYING', 'READY_TO_CLEANUP', 'PIKPAK_DELETING']
                    .includes(this.tasks.get(id)?.status)).slice(0, 20);
            for(let i = 0; i < transferring.length; i += 4) {
                await Promise.all(transferring.slice(i, i + 4).map(async id => {
                    try { this.downloads.set(id, await client.getDownloads(id)); }
                    catch (_) { /* Continue showing the last known transfer data. */ }
                }));
            }
            interval = ids.some(id => BRIDGE_ACTIVE.has(this.tasks.get(id)?.status)) ? 5000 :
                ids.some(id => !this.tasks.has(id)) ? 15000 : 0;
        } catch (error) {
            console.warn('[EhPikPakAria2] 查询 Bridge 下载进度失败', error);
            interval = 15000;
        } finally {
            this.render();
            this.polling = false;
            if(this.refreshAfterPoll) {
                this.refreshAfterPoll = false;
                this.schedule(0);
            } else if(interval) this.schedule(interval);
        }
    }
}

class TaskStatus {
    constructor() {
        this.element = document.createElement("div");
        this.element.className = 'aria2helper-status'
        this.monitorCount = 0;
    }
    setStatus(task) {
        this.monitorCount ++;
        const statusBox = this.element;
        statusBox.style.display = 'block'
        const completedLength = parseInt(task.completedLength, 10) || 0;
        const totalLength = parseInt(task.totalLength, 10) || 0;
        const downloadSpeed = parseInt(task.downloadSpeed, 10) || 0;
        const uploadLength = parseInt(task.uploadLength, 10) || 0;
        const uploadSpeed = parseInt(task.uploadSpeed, 10) || 0;
        const connections = parseInt(task.connections, 10) || 0;
        const file = task.files[0];
        const filePath = file ? file.path : '';
        const name = filePath.split(/[\/\\]/).pop();
        // 显示扩展名 用于区分当前下载的是种子 还是文件。
        const ext = name.includes(".") ? name.split('.').pop() : '';

        let progress = '-';

        if(totalLength) {
            progress = (completedLength/totalLength * 100).toFixed(2) + '%';
        }

        // ⠓ ⠚ ⠕ ⠪
        const iconList = "⠓⠋⠙⠚".split("");
        const icon = iconList[this.monitorCount % iconList.length];
        let info = [];

        if (task.status === 'active') {
            if (task.verifyIntegrityPending) {
                info.push(`<b>${icon} 🔍 等待验证</b>`);
            } else if (task.verifiedLength) {
                info.push(`<b>${icon} 🔍 正在验证</b>`);
                if (task.verifiedPercent) {
                    info.push(`已验证 (${task.verifiedPercent})`);
                }
            } else if (task.seeder === true || task.seeder === 'true') {
                info.push(`<b>${icon} 📤 做种</b>`);
                info.push(`已上传：${Tool.fileSize(uploadLength)}`);
                info.push(`速度：${Tool.fileSize(uploadSpeed)}/s`);
            } else {
                info.push(`<b>${icon} 📥 下载中</b>`);
                info.push(`进度：${progress}`);
                info.push(`速度：${Tool.fileSize(downloadSpeed)}/s`);
            }
        } else if (task.status === 'waiting') {
            info.push(`<b>${icon} ⏳ 排队</b>`);
        } else if (task.status === 'paused') {
            info.push(`<b>${icon} ⏸ 暂停</b>`);
            info.push(`进度：${progress}`);
        } else if (task.status === 'complete') {
            info.push(`<b>${icon} ☑️ 完成</b>`);
        } else if (task.status === 'error') {
            const errorMessageCN = ARIA2_ERROR_MSG[task.errorCode]
            info.push(`<b>${icon} 错误</b> (${task.errorCode}: ${errorMessageCN || task.errorMessage || "未知错误"})`);
            info.push(`进度：${progress}`);
        } else if (task.status === 'removed') {
            info.push(`<b>${icon} ⛔️ 已删除</b>`);
        }

        info.push(`类型：${ext}`);

        statusBox.innerHTML = info.map(v => `<span>${v}</span>`).join(' ');

        if(task.followedBy && task.followedBy.length) {
            // BT任务跟随
            Tool.setTaskId(GID, task.followedBy[0]);
        }
    }

}

class Tool {


    static htmlDecodeByRegExp (str) {
        let temp = "";
        if(str.length == 0) return "";
        temp = str.replace(/&amp;/g,"&");
        temp = temp.replace(/&lt;/g,"<");
        temp = temp.replace(/&gt;/g,">");
        temp = temp.replace(/&nbsp;/g," ");
        temp = temp.replace(/&#39;/g,"\'");
        temp = temp.replace(/&quot;/g,"\"");
        return temp;
    }

    static addStyle(styles) {
        var styleSheet = document.createElement("style")
        styleSheet.innerText = styles
        document.head.appendChild(styleSheet)
    }

    static urlGetGId(url) {
        let m;
        m = /gid=(\d+)/i.exec(url);
        if(m) return parseInt(m[1], 10);
        m = /archive\/(\d+)\//i.exec(url);
        if(m) return parseInt(m[1], 10);
        m = /\/g\/(\d+)\//i.exec(url);
        if(m) return parseInt(m[1], 10);
    }

    static urlGetToken(url) {
        let m;
        m = /&t(oken)?=(\d+)/i.exec(url);
        if(m) return m[2];
        m = /\/g\/(\d+)\/(\w+)\//i.exec(url);
        if(m) return m[2];
    }

    static setTaskId(ehGid, ariaGid) {
        GM_setValue("task-" + ehGid, ariaGid);
    }

    static getTaskId(ehGid) {
        return GM_getValue("task-" + ehGid, 0);
    }

    static fileSize(_size, round = 2) {
        const divider = 1024;

        if (_size < divider) {
          return _size + ' B';
        }

        if (_size < divider * divider && _size % divider === 0) {
          return (_size / divider).toFixed(0) + ' KB';
        }

        if (_size < divider * divider) {
          return `${(_size / divider).toFixed(round)} KB`;
        }

        if (_size < divider * divider * divider && _size % divider === 0) {
          return `${(_size / (divider * divider)).toFixed(0)} MB`;
        }

        if (_size < divider * divider * divider) {
          return `${(_size / divider / divider).toFixed(round)} MB`;
        }

        if (_size < divider * divider * divider * divider && _size % divider === 0) {
          return `${(_size / (divider * divider * divider)).toFixed(0)} GB`;
        }

        if (_size < divider * divider * divider * divider) {
          return `${(_size / divider / divider / divider).toFixed(round)} GB`;
        }

        if (_size < divider * divider * divider * divider * divider &&
          _size % divider === 0) {
          const r = _size / divider / divider / divider / divider;
          return `${r.toFixed(0)} TB`;
        }

        if (_size < divider * divider * divider * divider * divider) {
          const r = _size / divider / divider / divider / divider;
          return `${r.toFixed(round)} TB`;
        }

        if (_size < divider * divider * divider * divider * divider * divider &&
          _size % divider === 0) {
          const r = _size / divider / divider / divider / divider / divider;
          return `${r.toFixed(0)} PB`;
        } else {
          const r = _size / divider / divider / divider / divider / divider;
          return `${r.toFixed(round)} PB`;
        }
    }

}

class MonitorTask {
    constructor() {
        this.gids = [];
        this.taskIds = [];
        this.taskToGid = {};
        this.statusMap = {};
        this.timerId = 0;
        this.run = false;
    }

    start() {
        this.run = true;
        this.refreshTaskIds();
        this.loadStatus();
    }

    stop() {
        this.run = false;
        if(this.timerId)clearTimeout(this.timerId);
    }

    addGid(gid) {
        this.gids.push(gid);
        GM_addValueChangeListener("task-" + gid, () => {
            this.refreshTaskIds();
        });
        this.statusMap[gid] = new TaskStatus();
        return this.statusMap[gid];
    }

    refreshTaskIds() {
        if(!this.gids.length) return;
        this.taskIds = this.gids.map(v => {
            const agid = Tool.getTaskId(v);
            if(agid) this.taskToGid[agid] = v;
            return agid;
        }).filter(v => v);
    }

    async loadStatus() {
        if(!this.run) return;
        let hasActive = false;
        try {
            const batch = await ariaClient.batchTellStatus(this.taskIds);
            batch.forEach(task => {
                this.setStatusToUI(task);
                if(task) hasActive = hasActive || "active" === task.status;
            });
        } catch (error) {
            console.error(error);
        }
        this.timerId = setTimeout(() => {
            this.loadStatus()
        }, hasActive ? 500 : 5000);
    }

    setStatusToUI(task) {
        if(!task) return;
        const gid = this.taskToGid[task.gid];
        if(!gid) return;
        const ui = this.statusMap[gid];
        if(!ui) return;
        ui.setStatus(task);
    }
}

const GID = Tool.urlGetGId(window.location.href);
const TOKEN = Tool.urlGetToken(window.location.href);

let ariaClient;
const bridgeProgressMonitor = new BridgeProgressMonitor();

// Extended gallery metadata (.gl3e) is positioned and must not host flow-based progress widgets.
function appendBridgeGalleryStatus(row, gid, fallbackHost) {
    const contentHost = row.querySelector('.gl4e');
    (contentHost || fallbackHost).appendChild(bridgeProgressMonitor.watchGallery(gid));
}

console.log({GID, TOKEN});


function oneClickButton(gid, pageLink, archiverLink, fileInfo = null) {
    const oneClick = document.createElement('div');
    oneClick.textContent = "🡇";
    oneClick.title = "[Aria2] 一键下载";
    oneClick.classList.add("aria2helper-one-click");
    let loading = false;
    oneClick.onclick = async () => {
        if(loading === true) return;
        loading = true;
        try {
            const dir = await chooseAria2Directory(ariaClient.rpc, fileInfo);
            if(dir === null) return;
            oneClick.innerHTML = SVG_LOADING_ICON;
            if (pageLink && !archiverLink) {
                const g = await fetch(pageLink, { credentials: "include" }).then(v => v.text());
                const archiverLinkMatch = /'(https:\/\/e.hentai\.org\/archiver\.php?.*?)'/i.exec(g);
                archiverLink = Tool.htmlDecodeByRegExp(archiverLinkMatch[1]).replace("--", "-");
            }
            let formData = new FormData();
            formData.append("dltype", gmc.get('ONE_CLICK_DOWNLOAD_DLTYPE').slice(0, 3));
            formData.append("dlcheck","Download Original Archive");
            const archiverHtml = await fetch(
                archiverLink,
                {method: "POST", credentials: "include", body: formData}
            ).then(v => v.text());
            const downloadLinkMatch = /"(http.*?\.hath.network\/archive.*?)"/i.exec(archiverHtml);
            const downloadLink = downloadLinkMatch[1] + '?start=1';
            const taskId = await submitToAria2(downloadLink, dir);
            Tool.setTaskId(gid, taskId);
            oneClick.innerHTML = "✔";
            setTimeout(() => {
                oneClick.innerHTML = "🡇";
            }, 2000);
        } catch (error) {
            alert("一键下载失败:" + (error.message || String(error)));
            oneClick.innerHTML = "🡇";
        } finally {
            loading = false;
        }
    }
    return oneClick;
}


async function getTorrentList(gid, token, lifeTime = 0) {
    const html = await fetch(`${document.location.origin}/gallerytorrents.php?gid=${gid}&t=${token}`, {credentials: "include"}).then(v => v.text());
    const safeHtml = html.replace(/^.*<body>(.*)<\/body>.*$/igms,"$1").replace(/<script.*?>(.*?)<\/script>/igms, '');
    const dom = document.createElement('div')
    dom.innerHTML = safeHtml;
    const formList = [...dom.querySelectorAll("form")];
    const list = formList.map((e, i) => {
        const link = e.querySelector("table > tbody > tr:nth-child(3) > td > a");
        if(!link) return null;
        const posted = e.querySelector("table > tbody > tr:nth-child(1) > td:nth-child(1)");
        const size = e.querySelector("table > tbody > tr:nth-child(1) > td:nth-child(2)");
        const seeds = e.querySelector("table > tbody > tr:nth-child(1) > td:nth-child(4)");
        const peers = e.querySelector("table > tbody > tr:nth-child(1) > td:nth-child(5)");
        const downloads = e.querySelector("table > tbody > tr:nth-child(1) > td:nth-child(6)");
        const uploader = e.querySelector("table > tbody > tr:nth-child(2) > td:nth-child(1)");
        const getNumber = (text = '') => parseFloat((text.match(/[\d\.]+/) || [0])[0]);
        const getValueText = (text = '') => (text.match(/:(.*)/) || ['',''])[1].trim();
        const sizeText = getValueText(size.textContent);
        const sizeNumber = getNumber(sizeText);
        const unit = [sizeText.match(/[KMGT]i?B/) || ['']][0];
        const magnification = {
            "KB": 1000,
            "MB": 1000 * 1000,
            "GB": 1000 * 1000 * 1000,
            "TB": 1000 * 1000 * 1000 * 1000,
            "KiB": 1024,
            "MiB": 1024 * 1024,
            "GiB": 1024 * 1024 * 1024,
            "TiB": 1024 * 1024 * 1024 * 1024,
        }
        if(!magnification[unit]) {
            console.warn("未知单位: ", unit, size);
        }
        let bytes = magnification[unit] ? sizeNumber * magnification[unit] : -1;
        const time = new Date(getValueText(posted.textContent));
        return {
            index: i,
            time: time,
            readableTime: dateStr(time),
            size: sizeText,
            bytes: bytes,
            seeds: getNumber(seeds.textContent), // 做种
            peers: getNumber(peers.textContent), // 下载中
            downloads: getNumber(downloads.textContent), // 完成
            user: getValueText(uploader.textContent),
            name: link.textContent,
            link: link.getAttribute('href'),
            achievements: new Set(),
        }
    }).filter(v => v);

    let maxBytes = 0;
    let maxTime = 0;
    let maxSeeds = 0;
    let maxPeers = 0;
    let maxDownloads = 0;
    list.forEach(v => {
        maxBytes = Math.max(maxBytes, v.bytes);
        maxTime = Math.max(maxTime, v.time.getTime());
        maxSeeds = Math.max(maxSeeds, v.seeds);
        maxPeers = Math.max(maxPeers, v.peers);
        maxDownloads = Math.max(maxDownloads, v.downloads);
    });
    list.forEach(v => {
        const time = v.time.getTime();
        if(v.bytes == maxBytes) v.achievements.add("size");
        if(time == maxTime) v.achievements.add("time");
        if(v.seeds == maxSeeds && maxSeeds > 0) v.achievements.add("seeds");
        if(v.peers == maxPeers && maxPeers > 0) v.achievements.add("peers");
        if(v.downloads == maxDownloads && maxDownloads > 0) v.achievements.add("downloads");
        if(time < lifeTime) v.achievements.add("overdue");
    });

    list.sort((a,b) => {
        return b.time.getTime() - a.time.getTime();
    })

    console.log('list', list);
    return list;
}

function dateStr(date = new Date()){
    const today = new Date();
    const now = today.getTime();
    const time = Math.floor((now - date.getTime())/1000) + ( today.getTimezoneOffset() * 60);
    if(time <= 60){
        return '刚刚';
    }else if(time<=60*60){
        return Math.floor(time/60)+"分钟前";
    }else if(time<=60*60*24){
        return  Math.floor(time/60/60)+"小时前";
    }else if(time<=60*60*24*7) {
        return Math.floor(time/60/60/24) + "天前";
    }else if(time<=60*60*24*7*4) {
        return Math.floor(time/60/60/24/7) + "周前";
    }else if(time<=60*60*24*365) {
        return (date.getMonth()+1).toString().padStart(2, '0')+"月"+date.getDate().toString().padStart(2, '0')+"日"
    }
    return date.getFullYear()+'年';
}

function torrentActionsCell(item, gid) {
    const link = item.link;
    // Existing delegated click listeners still target the three button classes.
    return `<td class="bt-actions-cell nowrap"><div class="bt-actions-group" role="group" aria-label="种子操作">
        <div data-link="${link}" data-gid="${gid}" class="aria2helper-one-click bt-download-button bt" title="发送到 aria2" aria-label="发送到 aria2">🡇</div>
        <div data-link="${link}" data-gid="${gid}" class="aria2helper-one-click bt-copy-button icon bt" title="复制磁链" aria-label="复制磁链">✂</div>
        <div data-link="${link}" data-gid="${gid}" class="aria2helper-one-click bt-bridge-button bt" title="发送到 PikPak Bridge" aria-label="发送到 PikPak Bridge">P</div>
    </div></td>`;
}

function torrentListRow(item, gid, buttonLeft, twoLines, achievement) {
    const actions = torrentActionsCell(item, gid);
    const nameHtml = `<td class="bt-name"><a href="${item.link}">${item.name}</a></td>`;
    const infoHtml = `<td class="bt-size nowrap"><span class="${achievement(item, 'size')}">${item.size}</span></td>
        <td class="bt-time nowrap"><span title="${item.time.toLocaleString()}" class="${achievement(item, 'time')}">${item.readableTime}</span></td>
        <td class="bt-seeds nowrap"><span class="${achievement(item, 'seeds')}">${item.seeds}</span></td>
        <td class="bt-peers nowrap"><span class="${achievement(item, 'peers')}">${item.peers}</span></td>
        <td class="bt-downloads nowrap"><span class="${achievement(item, 'downloads')}">${item.downloads}</span></td>`;
    if(twoLines) {
        return `<tr class="bt-item no-hover">
            <td class="bt-name" colspan="6"><a href="${item.link}">${item.name}</a></td>
        </tr>
        <tr class="bt-item">
            ${actions}
            ${infoHtml}
        </tr>`;
    }
    return `<tr class="bt-item">
        ${buttonLeft ? actions : ''}
        ${nameHtml}
        ${infoHtml}
        ${buttonLeft ? '' : actions}
    </tr>`;
}

async function torrentsPopDetail(btButtonBox, gid = GID, token = TOKEN, buttonLeft = false, twoLines = false) {
    if(!btButtonBox) {
        btButtonBox = document.querySelector('#gd5 .g2:nth-child(3)');
    }
    if(btButtonBox) {
        boxA = btButtonBox.querySelector('a');
        boxA.onmouseenter = async () => {
            let btListBox = btButtonBox.querySelector('#btList');
            btButtonBox.classList.add('btListShow');
            if(!btListBox) {
                btListBox = document.createElement("div");
                btListBox.id = 'btList';
                btButtonBox.appendChild(btListBox);
                btListBox.innerHTML = SVG_LOADING_ICON;
                try {
                    const torents = await getTorrentList(gid, token);
                    if(torents.length) {
                        const achievement = (item, name) => item.achievements.has(name) ? 'quality' : '';
                        let th = '';
                        if(twoLines) {
                            th = `
                            <th>名称</th>
                            <th>体积</th>
                            <th>时间</th>
                            <th><span title="正在做种 Seeds">📤</span></th>
                            <th><span title="正在下载 Peers">📥</span></th>
                            <th><span title="下载完成 Downloads">✔️</span></th>`;
                        }else {
                            th = `
                            ${buttonLeft ? "<th></th>" : ""}
                            <th>名称</th>
                            <th>体积</th>
                            <th>时间</th>
                            <th><span title="正在做种 Seeds">📤</span></th>
                            <th><span title="正在下载 Peers">📥</span></th>
                            <th><span title="下载完成 Downloads">✔️</span></th>
                            ${buttonLeft ? "" : "<th></th>"}`;
                        }


                        btListBox.innerHTML = `<table>
                        <tr>
                        ${th}
                        </tr>
                        ${
                            torents.map(item => torrentListRow(item, gid, buttonLeft, twoLines, achievement)).join('')
                        }</table>`;

                        const cells = btListBox.querySelectorAll('td.bt-actions-cell');
                        torents.forEach((item, index) => {
                            if(cells[index]) cells[index].appendChild(bridgeProgressMonitor.watchTorrent(item.link));
                        });

                        btListBox.onclick = async (event) => {
                            const bridgeButton = event.target.closest && event.target.closest('.bt-bridge-button');
                            if(bridgeButton && btListBox.contains(bridgeButton)) {
                                event.preventDefault();
                                const info = torrentFileInfo(torents.find(item => item.link === bridgeButton.dataset.link), gid);
                                await sendTorrentToBridge(bridgeButton.dataset.link, bridgeButton, info);
                                return;
                            }
                            const ariaButton = event.target.closest && event.target.closest('.bt-download-button');
                            if(ariaButton && btListBox.contains(ariaButton)) {
                                event.preventDefault();
                                if(ariaButton.dataset.loading === '1') return;
                                ariaButton.dataset.loading = '1';
                                try {
                                    const info = torrentFileInfo(torents.find(item => item.link === ariaButton.dataset.link), gid);
                                    const dir = await chooseAria2Directory(ariaClient.rpc, info);
                                    if(dir === null) return;
                                    ariaButton.innerHTML = SVG_LOADING_ICON;
                                    const link = ariaButton.dataset.link;
                                    const torrentGid = parseInt(ariaButton.dataset.gid, 10);
                                    const taskId = await submitToAria2(getTorrentLink(link), dir);
                                    Tool.setTaskId(torrentGid, taskId);
                                    ariaButton.innerHTML = "✔";
                                    setTimeout(() => {
                                        if(ariaButton.dataset.loading !== '1') ariaButton.innerHTML = "🡇";
                                    }, 2000);
                                } catch (error) {
                                    alert("一键下载失败:" + (error.message || String(error)));
                                    ariaButton.innerHTML = "🡇";
                                } finally {
                                    ariaButton.dataset.loading = '0';
                                }
                                return;
                            }
                            const copyButton = event.target.closest && event.target.closest('.bt-copy-button');
                            if(copyButton && btListBox.contains(copyButton)) {
                                event.preventDefault();
                                await copyTorrentMagnetToClipboard(copyButton.dataset.link, copyButton);
                                return;
                            }

                        }
                    }else {
                        btListBox.innerHTML = "没有可用种子"
                    }
                } catch (error) {
                    btListBox.innerHTML = error.message;
                }
            }
        }
        btButtonBox.onmouseleave = () => {
            btButtonBox.classList.remove('btListShow');
        }
    }
}

function getTorrentInfo(link) {
    let match = link.match(/\/(\d+)\/([0-9a-f]{40})/i);
    if(!match) return;
    return {
        hash: match[2],
        trackerId: match[1],
    }
}

function torrentLink2magnet (link) {
    const info = getTorrentInfo(link);
    if(!info) return;
    return `magnet:?xt=urn:btih:${info.hash}&tr=${encodeURIComponent(`http://ehtracker.org/${info.trackerId}/announce`)}`;
}

function torrentLinkForceEhTracker (link) {
    const info = getTorrentInfo(link);
    if(!info) return;
    return `https://ehtracker.org/get/${info.trackerId}/${info.hash}.torrent`;
}

function getTorrentLink(link) {
    if(gmc.get('USE_MAGNET')) {
        return torrentLink2magnet(link) || link;
    }
    if(link.includes('exhentai.org') && gmc.get('REPLACE_EX_TORRENT_URL')) {
        return torrentLinkForceEhTracker(link) || link;
    }
    return link;
}


// Preserve site-native Copy Magnet / Information nodes, form ownership,
// event handlers and submitted values. Do not reconstruct the original buttons.
function torrentPageNativeActionType(node) {
    const tag = node?.tagName?.toUpperCase();
    if(!['INPUT', 'BUTTON', 'A'].includes(tag)) return '';
    const label = [node.value, node.textContent, node.title,
        node.getAttribute?.('aria-label')].filter(Boolean).join(' ');
    if(/copy\s*(?:magnet|magnetic)|(?:复制|拷贝).*(?:磁|magnet)/i.test(label)) return 'copy';
    if(/\binformation\b|\binfo\b|(?:详细信息|种子信息|详细资料|详情)/i.test(label)) return 'info';
    return '';
}

function arrangeTorrentPageActions(insertionPoint, widget) {
    const host = insertionPoint?.parentNode;
    if(!host || !widget?.element || widget.element.parentNode !== host) return false;
    if(host.classList?.contains('aria2helper-torrent-actions-grid') ||
       host.className === 'aria2helper-torrent-actions-grid') return false;

    // Only reparent direct siblings inside their existing form/container.
    // If the native controls aren't identifiable, leave the original layout intact.
    const native = Array.from(host.children).filter(node => node !== widget.element);
    const copy = native.find(node => torrentPageNativeActionType(node) === 'copy');
    const info = native.find(node => torrentPageNativeActionType(node) === 'info');
    if(!copy || !info || copy === info ||
        host.querySelector?.('.aria2helper-torrent-actions-grid')) return false;

    const grid = document.createElement('div');
    grid.className = 'aria2helper-torrent-actions-grid';
    host.insertBefore(grid, widget.element);
    grid.appendChild(widget.element);
    grid.appendChild(copy);
    grid.appendChild(info);
    copy.classList.add('aria2helper-native-action', 'aria2helper-native-copy');
    info.classList.add('aria2helper-native-action', 'aria2helper-native-info');
    // The host is normally a <td>. Resolve an ancestor TD defensively so the
    // width reservation also works when a site wrapper is inserted.
    let cell = host;
    while(cell && cell.tagName?.toUpperCase() !== 'TD') cell = cell.parentNode;
    if(cell) cell.classList.add('aria2helper-torrent-actions-cell');
    if(!copy.title) copy.title = '复制磁力链';
    if(!info.title) info.title = '查看种子详细信息';

    widget.button.value = 'Aria2';
    widget.button.title = '发送到 aria2';
    widget.bridgeButton.value = 'PikPak';
    widget.bridgeButton.title = '发送到 PikPak Bridge';
    return true;
}

function init() {
    ariaClient = new AriaClientLite({rpc: gmc.get('ARIA2_RPC'), secret: gmc.get('ARIA2_SECRET'), id: ARIA2_CLIENT_ID});
    Tool.addStyle(STYLE);
    Tool.addStyle(ONE_CLICK_STYLE);
    Tool.addStyle(BRIDGE_TARGET_STYLE);

    const monitorTask = new MonitorTask();
    if(GID) {

        // button
        if(IS_TORRENT_PAGE) {
            let tableList = document.querySelectorAll("#torrentinfo form table");
            if(tableList && tableList.length){
                tableList.forEach(function (table) {
                    let insertionPoint = table.querySelector('input[type="submit"],button[type="submit"]');
                    if(!insertionPoint)return;
                    let a = table.querySelector('a');
                    if(!a) return;
                    const link = a.href;
                    const button = new SendTaskButton(GID, link, torrentPageFileInfo(table));
                    insertionPoint.parentNode.insertBefore(button.element, insertionPoint);
                    arrangeTorrentPageActions(insertionPoint, button);
                    const actions = button.element.parentNode;
                    const statusHost = actions?.classList?.contains('aria2helper-torrent-actions-grid') ?
                        actions.parentNode : actions;
                    if(statusHost) statusHost.appendChild(bridgeProgressMonitor.watchTorrent(link));
                });
            }
        }

        if (IS_HATH_ARCHIVE_PAGE) {
            let insertionPoint = document.querySelector("#db a");
            if(!insertionPoint)return;
            const link = insertionPoint.href;
            const button = new SendTaskButton(GID, link, archiveFileInfo());
            button.element.style.marginTop = '16px';
            insertionPoint.parentNode.insertBefore(button.element, insertionPoint);
        }

        // 状态监听
        const taskStatusUi = monitorTask.addGid(GID);
        if (IS_HATH_ARCHIVE_PAGE && gmc.get('USE_HATH_ARCHIVE_TASK_STATUS')) {
            taskStatusUi.element.style.marginTop = '8px';
            const insertionPoint = document.querySelector('#db strong');
            if(insertionPoint) insertionPoint.parentElement.insertBefore(taskStatusUi.element, insertionPoint.nextElementSibling);
        }
        if (IS_GALLERY_DETAIL_PAGE && gmc.get('USE_GALLERY_DETAIL_TASK_STATUS')) {
            const insertionPoint = document.querySelector('#gd2');
            if(insertionPoint) insertionPoint.appendChild(taskStatusUi.element);
        }
        if (IS_TORRENT_PAGE && gmc.get('USE_TORRENT_TASK_STATUS')) {
            const insertionPoint = document.querySelector('#torrentinfo p');
            if(insertionPoint) insertionPoint.parentElement.insertBefore(taskStatusUi.element, insertionPoint.nextElementSibling);
        }
        if(IS_GALLERY_DETAIL_PAGE) {
            const insertionPoint = document.querySelector('#gd2');
            if(insertionPoint) insertionPoint.appendChild(bridgeProgressMonitor.watchGallery(GID));
        }
        if(IS_GALLERY_DETAIL_PAGE && gmc.get('USE_TORRENT_POP_LIST')) {
            torrentsPopDetail();
        }
    } else if(gmc.get('USE_LIST_TASK_STATUS') || gmc.get('BRIDGE_URL')) {
        const trList = document.querySelectorAll(".itg tr, .itg .gl1t");
        if(trList && trList.length) {
            const insertionPointMap = {};
            let textAlign = 'left';
            trList.forEach(function (tr) {
                let glname = tr.querySelector(".gl3e, .glname");
                let a = tr.querySelector(".glname a, .gl1e a, .gl1t");
                if(tr.classList.contains('gl1t')) {
                    glname = tr;
                    a = tr.querySelector('a');
                    textAlign = 'center';
                }
                if(!(glname && a)) return;
                const gid = Tool.urlGetGId(a.href);
                const token = Tool.urlGetToken(a.href);
                insertionPointMap[gid] = glname;
                if(gmc.get('USE_LIST_TASK_STATUS')) {
                    const statusUI = monitorTask.addGid(gid);
                    statusUI.element.style.textAlign = textAlign;
                    glname.appendChild(statusUI.element);
                }
                appendBridgeGalleryStatus(tr, gid, glname);

                const listTypeDom = document.querySelector("#dms select > option[selected]");
                const listType = listTypeDom ? listTypeDom.value : '';
                if(listType == 't') return; // 暂时不支持缩略图模式,显示问题
                const gldown = tr.querySelector(".gldown");
                const torrentImg = gldown.querySelector('img');
                const torrentImgSrc = torrentImg.attributes.getNamedItem('src').value
                const hasTorrent = torrentImgSrc.includes("g/t.png");
                if(gmc.get('USE_TORRENT_POP_LIST') && hasTorrent) {
                    torrentsPopDetail(gldown, gid, token, true, listType == 't');
                }
            });
        }
    }


    monitorTask.start();

    if(gmc.get('USE_ONE_CLICK_DOWNLOAD') && gmc.get('ARIA2_RPC')) {
        const trList = document.querySelectorAll(".itg tr, .itg .gl1t");
        if(trList && trList.length) {
            trList.forEach(tr => {
                let a = tr.querySelector(".glname a, .gl1e a, .gl1t");
                if(tr.classList.contains('gl1t')) a = tr.querySelector('a');
                if(!a) return;
                const link = a.href;
                const gid = Tool.urlGetGId(a.href);
                const gldown = tr.querySelector(".gldown");
                if(!gldown) return;
                const fileInfo = galleryListArchiveFileInfo(tr);
                gldown.appendChild(oneClickButton(gid, link, null, fileInfo));
            })
        }
        if(IS_GALLERY_DETAIL_PAGE) {
            const gldown = document.querySelector(".g2.gsp");
            const a = document.querySelector(".g2.gsp a");
            const archiverLinkMatch = /'(https:\/\/e.hentai\.org\/archiver\.php?.*?)'/i.exec(a.onclick.toString());
            const archiverLink = Tool.htmlDecodeByRegExp(archiverLinkMatch[1]).replace("--", "-");
            gldown.appendChild(oneClickButton(GID, null, archiverLink, archiveFileInfo()));
        }
    }

}
