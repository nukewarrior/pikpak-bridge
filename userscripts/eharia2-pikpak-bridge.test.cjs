'use strict';

const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const {createHash, webcrypto} = require('node:crypto');

const script = fs.readFileSync(path.join(__dirname, 'eharia2-pikpak-bridge.user.js'), 'utf8');
const hexHash = '0123456789abcdef0123456789abcdef01234567';

function createHarness(respond, storage = new Map()) {
    const requests = [];
    const clipboard = [];
    const alerts = [];
    const delayedUpdates = [];
    const config = {
        ARIA2_RPC: '',
        BRIDGE_URL: 'https://bridge.example.test/base/'
    };
    class StubGMConfig {
        constructor(options) { this.options = options; }
        get(key) {
            return Object.hasOwn(config, key) ? config[key] : this.options.fields[key]?.default;
        }
        open() {}
    }
    const ctx = {
        URL,
        TextDecoder,
        crypto: webcrypto,
        window: {location: {
            host: 'e-hentai.org', href: 'https://e-hentai.org/g/123/abc/',
            pathname: '/g/123/abc/'
        }},
        GM_config: StubGMConfig,
        GM_registerMenuCommand() {},
        GM_getValue(key, fallback) { return storage.has(key) ? storage.get(key) : fallback; },
        GM_setValue(key, value) { storage.set(key, value); },
        GM_setClipboard(value, type) { clipboard.push({value, type}); },
        GM_xmlhttpRequest(req) {
            requests.push(req);
            respond(req);
        },
        setTimeout(fn, wait) {
            if(wait === 2000) { delayedUpdates.push(fn); return 0; }
            return wait === 3000 ? 0 : setTimeout(fn, wait);
        },
        clearTimeout,
        console: {log() {}, warn() {}, error() {}},
        alert(message) { alerts.push(message); }
    };
    vm.createContext(ctx);
    vm.runInContext(script, ctx, {filename: 'eharia2-pikpak-bridge.user.js'});
    return {ctx, requests, config, clipboard, alerts, delayedUpdates, storage};
}

function evaluate(ctx, expression) {
    return vm.runInContext(expression, ctx);
}

function exampleTorrent() {
    // Byte-exact BitTorrent v1 metadata, separate from any URL identifier.
    const announce = 'https://ehtracker.org/123/announce';
    const info = Buffer.from('d6:lengthi1e4:name4:test12:piece lengthi16384e6:pieces20:abcdefghijklmnopqrste');
    const torrent = Buffer.concat([
        Buffer.from('d8:announce' + Buffer.byteLength(announce) + ':' + announce + '4:info'),
        info,
        Buffer.from('e')
    ]);
    return {
        arrayBuffer: Uint8Array.from(torrent).buffer,
        hash: createHash('sha1').update(info).digest('hex').toUpperCase(),
        announce
    };
}

function respondWithTorrent(req, other) {
    if(req.responseType === 'arraybuffer') {
        req.onload({status: 200, response: exampleTorrent().arrayBuffer});
    } else {
        other(req);
    }
}

test('torrent URL is fetched; BTIH is computed from exact info bytes rather than URL', async () => {
    const sample = exampleTorrent();
    const {ctx, requests} = createHarness(req => req.onload({status: 200, response: sample.arrayBuffer}));
    const magnet = await evaluate(ctx, "bridgeMagnetFromTorrentLink('https://exhentai.org/torrent/123/" + hexHash + ".torrent')");
    assert.equal(magnet, 'magnet:?xt=urn:btih:' + sample.hash + '&tr=' + encodeURIComponent(sample.announce));
    assert.notEqual(sample.hash, hexHash.toUpperCase());
    assert.equal(requests[0].responseType, 'arraybuffer');
    assert.equal(requests[0].url, 'https://exhentai.org/torrent/123/' + hexHash + '.torrent');
});

test('short and legacy torrent URLs use their actual file contents', async () => {
    const {ctx} = createHarness(req => req.onload({status: 200, response: exampleTorrent().arrayBuffer}));
    const magnet = await evaluate(ctx, "bridgeMagnetFromTorrentLink('https://e-hentai.org/torrent/123/abcd.torrent')");
    assert.match(magnet, new RegExp(exampleTorrent().hash));
    const second = await evaluate(ctx, "bridgeMagnetFromTorrentLink('https://ehtracker.org/get/123/" + hexHash + ".torrent')");
    assert.equal(second, magnet);
});

test('bad bencode and v2-only torrent cannot create a misleading BTIH', async () => {
    const html = createHarness(req => req.onload({status: 200, response: Uint8Array.from(Buffer.from('<html>Login</html>')).buffer}));
    await assert.rejects(evaluate(html.ctx, "bridgeMagnetFromTorrentLink('https://e-hentai.org/torrent/123/abcd.torrent')"), /BitTorrent|种子/);
    const v2 = Buffer.from('d4:infod4:name4:test12:meta versioni2eee');
    const v2Ctx = createHarness(req => req.onload({status: 200, response: Uint8Array.from(v2).buffer}));
    await assert.rejects(evaluate(v2Ctx.ctx, "bridgeMagnetFromTorrentLink('https://e-hentai.org/torrent/123/abcd.torrent')"), /BT v1/);
});

test('direct magnet works without fetching; untrusted torrent hosts fail closed', async () => {
    const {ctx, requests} = createHarness(() => {});
    const magnet = 'magnet:?xt=urn:btih:' + hexHash;
    assert.equal(await evaluate(ctx, 'bridgeMagnetFromTorrentLink(' + JSON.stringify(magnet) + ')'), magnet);
    await assert.rejects(evaluate(ctx, "bridgeMagnetFromTorrentLink('https://evil.example/get/123/abc.torrent')"), /不允许/);
    await assert.rejects(evaluate(ctx, "bridgeMagnetFromTorrentLink('magnet:?xt=urn:btih:WRONG')"), /BTIH/);
    assert.equal(requests.length, 0);
});

test('torrent download HTTP errors and network failures are reported', async () => {
    for(const [handler, error] of [
        [req=>req.onload({status: 403, response:null}),/HTTP 403/],
        [req=>req.onerror({}),/权限/],
        [req=>req.ontimeout({}),/超时/]
    ]) {
        const {ctx} = createHarness(handler);
        await assert.rejects(evaluate(ctx, "bridgeMagnetFromTorrentLink('https://ehtracker.org/get/123/abc.torrent')"),error);
    }
});

test('bridge URL rejects unsafe schemes, credentials and query parameters', () => {
    const {ctx} = createHarness(() => {});
    for (const invalid of [
        'file:///etc/passwd', 'ftp://example.org',
        'https://user:password@example.org', 'https://example.org/?token=a'
    ]) {
        assert.throws(() => evaluate(ctx, 'new PikPakBridgeClient(' + JSON.stringify(invalid) + ')'), /HTTP|地址/);
    }
    assert.equal(evaluate(ctx, "new PikPakBridgeClient('https://bridge.example.org/path///').baseURL"), 'https://bridge.example.org/path');
});

test('201 creates exactly one Bridge task with magnet and selected target', async () => {
    const {ctx, requests} = createHarness(req => req.onload({
        status: 201, responseText: JSON.stringify({id: 'task-123', status: 'QUEUED'})
    }));
    const result = await evaluate(ctx, "new PikPakBridgeClient('https://bridge.example.test/base/').addTask('magnet:?xt=urn:btih:" + hexHash + "', 'movies')");
    assert.equal(result.id, 'task-123');
    assert.equal(result.duplicate, false);
    assert.equal(requests.length, 1);
    assert.equal(requests[0].method, 'POST');
    assert.equal(requests[0].url, 'https://bridge.example.test/base/api/v1/tasks');
    assert.equal(requests[0].headers['Content-Type'], 'application/json');
    const body = JSON.parse(requests[0].data);
    assert.equal(body.url, 'magnet:?xt=urn:btih:' + hexHash);
    assert.equal(body.target, 'movies');
});

test('empty target uses the bridge default target', async () => {
    const {ctx, requests} = createHarness(req => req.onload({status: 201, responseText:'{"id":"task-default"}'}));
    await evaluate(ctx, "new PikPakBridgeClient('http://192.168.1.10:8080').addTask('magnet:?xt=urn:btih:" + hexHash + "', '')");
    assert.deepEqual(JSON.parse(requests[0].data), {url:'magnet:?xt=urn:btih:' + hexHash});
});

test('409 duplicate is handled as existing task rather than submission failure', async () => {
    const {ctx} = createHarness(req => req.onload({
        status:409, responseText:JSON.stringify({
            error:'task already exists',existing_task_id:'old-task',
            status:'PIKPAK_RUNNING',target_id:'tv'
        })
    }));
    const result=await evaluate(ctx, "new PikPakBridgeClient('https://bridge.example.org').addTask('magnet:?xt=urn:btih:" + hexHash + "')");
    assert.equal(result.duplicate, true);
    assert.equal(result.id, 'old-task');
    assert.equal(result.target, 'tv');
});

test('server error, malformed JSON, network error and timeout reject', async () => {
    const cases = [
        [req=>req.onload({status:400,responseText:'{"error":"bad magnet"}'}), /HTTP 400.*bad magnet/],
        [req=>req.onload({status:200,responseText:'<html>error</html>'}), /JSON/],
        [req=>req.onerror({}), /无法连接/],
        [req=>req.ontimeout({}), /超时/]
    ];
    for(const [handler,pattern] of cases) {
        const {ctx}=createHarness(handler);
        await assert.rejects(evaluate(ctx, "new PikPakBridgeClient('https://bridge.example.org').addTask('magnet:?xt=urn:btih:" + hexHash + "')"),pattern);
    }
});


async function waitForDialog(doc) {
    for(let i=0;i<100;i++) {
        if(doc.body.children.length) return;
        await new Promise(resolve => setImmediate(resolve));
    }
    throw new Error('Target selection dialog did not appear');
}

function fakeDocument() {
    const listeners = new Map();
    const body = element('body');
    const doc = {
        body,
        activeElement: {focus() {}},
        createElement: element,
        addEventListener(event, handler) { listeners.set(event, handler); },
        removeEventListener(event, handler) {
            if(listeners.get(event) === handler) listeners.delete(event);
        },
        dispatchKey(event) { listeners.get('keydown')?.(event); },
        listeners
    };
    return doc;

    function element(tagName) {
        return {
            tagName: tagName.toUpperCase(),
            children: [],
            dataset: {},
            value: '',
            appendChild(child) { child.parentNode = this; this.children.push(child); },
            remove() {
                if(this.parentNode) {
                    this.parentNode.children = this.parentNode.children.filter(child => child !== this);
                }
            },
            setAttribute(name, value) { this[name] = value; },
            focus() {}
        };
    }
}

test('configuration needs only Bridge URL and fetches enabled targets dynamically', async () => {
    const {ctx, requests} = createHarness(req => req.onload({
        status:200, responseText: JSON.stringify({targets:[
            {id:'movies',name:'电影',dir:'/downloads/movies',enabled:true,default:true},
            {id:'tv',name:'电视剧',dir:'/downloads/tv'},
            {id:'hidden',name:'禁用',enabled:false}
        ]})
    }));
    assert.equal(evaluate(ctx, "gmc.options.fields.BRIDGE_TARGET"), undefined);
    const targets = await evaluate(ctx, "new PikPakBridgeClient('https://bridge.example.test/base/').listTargets()");
    assert.equal(requests.length, 1);
    assert.equal(requests[0].method, 'GET');
    assert.equal(requests[0].url, 'https://bridge.example.test/base/api/v1/targets');
    assert.deepEqual(Array.from(targets, x => x.id), ['movies','tv']);
    assert.equal(targets[0].default,true);
    assert.equal(targets[0].dir,'/downloads/movies');
});

test('unconfigured Bridge, invalid response and unreachable Bridge show actionable errors', async () => {
    const cases = [
        [req=>req.onload({status:200,responseText: '{"targets":[]}' }), /没有可用的下载目标/],
        [req=>req.onload({status:200,responseText: '{"targets":{}}' }), /格式无效/],
        [req=>req.onload({status:502,responseText: '{"error":"upstream unavailable"}' }), /502.*upstream unavailable/],
        [req=>req.onload({status:200,responseText: '<html>login</html>' }), /JSON/],
        [req=>req.onerror({}), /无法连接/],
        [req=>req.ontimeout({}), /超时/]
    ];
    for(const [callback, re] of cases) {
        const {ctx} = createHarness(callback);
        await assert.rejects(evaluate(ctx,"new PikPakBridgeClient('https://bridge.example.org').listTargets()"),re);
    }
});

test('one available target submits automatically without asking or config target', async () => {
    const {ctx, requests, storage} = createHarness(req => respondWithTorrent(req, request => {
        if(request.method === 'GET') {
            request.onload({status:200,responseText:JSON.stringify({targets:[{id:'movies',name:'电影'}]})});
        } else {
            request.onload({status:201,responseText:'{"id":"task-123","status":"QUEUED"}'});
        }
    }));
    const button = {tagName:'INPUT',dataset:{},value:'发送到 PikPak',disabled:false,title:''};
    ctx.testButton = button;
    await evaluate(ctx,"sendTorrentToBridge('https://exhentai.org/torrent/123/" + hexHash + "/file.torrent', testButton)");
    assert.equal(requests.length,3);
    assert.equal(requests[0].responseType,'arraybuffer');
    assert.equal(requests[1].method,'GET');
    assert.equal(requests[2].method,'POST');
    assert.equal(JSON.parse(requests[2].data).target,'movies');
    assert.equal(storage.get('PIKPAK_BRIDGE_LAST_TARGET:https://bridge.example.test/base'), 'movies');
    assert.equal(button.value,'已提交');
    assert.equal(button.disabled,false);
});

test('multiple targets present a dialog with server-provided names; selected ID is submitted', async () => {
    const {ctx, requests, storage} = createHarness(req => respondWithTorrent(req, request => {
        if(request.method === 'GET') {
            request.onload({status:200,responseText:JSON.stringify({targets:[
                {id:'movies',name:'电影',dir:'/downloads/movies',default:true},
                {id:'tv',name:'电视剧',dir:'/downloads/tv'}
            ]})});
        } else {
            request.onload({status:201,responseText:'{"id":"task-choose"}'});
        }
    }));
    const doc = fakeDocument(); ctx.document=doc;
    const button = {tagName:'INPUT',dataset:{},value:'发送到 PikPak',disabled:false,title:''};
    ctx.testButton=button;
    const sending = evaluate(ctx,"sendTorrentToBridge('https://exhentai.org/torrent/123/" + hexHash + "/file.torrent', testButton)");
    await waitForDialog(doc);
    assert.equal(doc.body.children.length,1);
    const dialog=doc.body.children[0].children[0];
    assert.equal(dialog['role'],'dialog');
    const select=dialog.children[2];
    assert.equal(select.value,'movies');
    assert.match(select.children[1].textContent,/电视剧/);
    assert.doesNotMatch(select.children[0].textContent,/默认/);
    select.value='tv';
    dialog.children[3].children[1].onclick();
    await sending;
    assert.equal(requests.filter(x=>x.method==='POST').length,1);
    assert.equal(JSON.parse(requests[2].data).target,'tv');
    assert.equal(storage.get('PIKPAK_BRIDGE_LAST_TARGET:https://bridge.example.test/base'), 'tv');
    assert.equal(doc.body.children.length,0);
    assert.equal(doc.listeners.size,0);
});

test('cancelling target dialog does not create Bridge task', async () => {
    const {ctx, requests, storage} = createHarness(req => respondWithTorrent(req, request => request.onload({
        status:200,responseText:JSON.stringify({targets:[{id:'a'},{id:'b'}]})
    })));
    const doc=fakeDocument();ctx.document=doc;
    const button={tagName:'INPUT',dataset:{},value:'发送到 PikPak',disabled:false,title:''};
    ctx.testButton=button;
    const sending=evaluate(ctx,"sendTorrentToBridge('https://exhentai.org/torrent/123/" + hexHash + "/file.torrent', testButton)");
    await waitForDialog(doc);
    const cancel=doc.body.children[0].children[0].children[3].children[0];
    cancel.onclick();
    await sending;
    assert.equal(requests.length,2);
    assert.equal(requests.filter(x=>x.method==='POST').length,0);
    assert.equal(storage.has('PIKPAK_BRIDGE_LAST_TARGET:https://bridge.example.test/base'),false);
    assert.equal(button.value,'发送到 PikPak');
    assert.equal(button.disabled,false);
    assert.equal(doc.body.children.length,0);
});


test('last successfully used target is selected after reloading the user script', async () => {
    const storage = new Map([['PIKPAK_BRIDGE_LAST_TARGET:https://bridge.example.test/base','tv']]);
    const {ctx, requests} = createHarness(req => respondWithTorrent(req, request => {
        if(request.method === 'GET') {
            request.onload({status:200,responseText:JSON.stringify({targets:[
                {id:'movies',name:'电影',dir:'/movies',default:true},
                {id:'tv',name:'电视剧',dir:'/tv'}
            ]})});
        } else {
            request.onload({status:201,responseText:'{"id":"task-reloaded"}'});
        }
    }), storage);
    const doc=fakeDocument();ctx.document=doc;
    const button={tagName:'INPUT',dataset:{},value:'发送到 PikPak',disabled:false,title:''};
    ctx.testButton=button;
    const sending=evaluate(ctx,"sendTorrentToBridge('https://exhentai.org/torrent/123/" + hexHash + "/file.torrent', testButton)");
    await waitForDialog(doc);
    const dialog=doc.body.children[0].children[0];
    const select=dialog.children[2];
    assert.equal(select.value,'tv', 'last used target should override server default');
    assert.doesNotMatch(select.children[0].textContent,/默认/);
    assert.doesNotMatch(select.children[1].textContent,/默认/);
    dialog.children[3].children[1].onclick();
    await sending;
    assert.equal(JSON.parse(requests[2].data).target,'tv');
    assert.equal(storage.get('PIKPAK_BRIDGE_LAST_TARGET:https://bridge.example.test/base'),'tv');
});

test('missing or disabled last target falls back to server default, then first available', () => {
    const storage=new Map([['PIKPAK_BRIDGE_LAST_TARGET:https://bridge.example.test/base','removed']]);
    const {ctx}=createHarness(()=>{},storage);
    ctx.targets=[
        {id:'first',name:'First',default:false},
        {id:'default',name:'Default',default:true}
    ];
    const preferred = "preferredBridgeTarget(targets,'https://bridge.example.test/base').id";
    assert.equal(evaluate(ctx, preferred), 'default');
    ctx.targets=[{id:'first',name:'First'},{id:'other',name:'Other'}];
    assert.equal(evaluate(ctx, preferred),'first');
    storage.set('PIKPAK_BRIDGE_LAST_TARGET:https://bridge.example.test/base','other');
    assert.equal(evaluate(ctx, preferred),'other');
    // Memory for a second Bridge must not leak into this service.
    assert.equal(evaluate(ctx,"preferredBridgeTarget(targets,'https://another-bridge.example.test').id"),'first');
});

test('duplicate and failed submissions must not overwrite the last successful target', async () => {
    const storage=new Map([['PIKPAK_BRIDGE_LAST_TARGET:https://bridge.example.test/base','tv']]);
    const baseKey='PIKPAK_BRIDGE_LAST_TARGET:https://bridge.example.test/base';
    const handlers=[
        req=>req.onload({status:409,responseText:'{"existing_task_id":"old-task","status":"PIKPAK_RUNNING","target_id":"tv"}'}),
        req=>req.onload({status:503,responseText:'{"error":"offline"}'})
    ];
    for(const respondSubmit of handlers) {
        const {ctx,alerts}=createHarness(req => respondWithTorrent(req, request => {
            if(request.method==='GET') {
                request.onload({status:200,responseText:JSON.stringify({targets:[{id:'movies',default:true},{id:'tv'}]})});
            } else respondSubmit(request);
        }),storage);
        const doc=fakeDocument();ctx.document=doc;
        const button={tagName:'INPUT',dataset:{},value:'发送到 PikPak',disabled:false,title:''};
        ctx.testButton=button;
        const sending=evaluate(ctx,"sendTorrentToBridge('https://exhentai.org/torrent/123/" + hexHash + "/file.torrent', testButton)");
        await waitForDialog(doc);
        // User chose a different target, but request did not create a new task.
        doc.body.children[0].children[0].children[2].value='movies';
        doc.body.children[0].children[0].children[3].children[1].onclick();
        await sending;
        assert.equal(storage.get(baseKey),'tv');
        assert.equal(doc.body.children.length,0);
        assert.ok(alerts.length<=1);
    }
});


test('aria2 directory dialog is required on first use and cancellation is harmless', async () => {
    const storage=new Map();
    const {ctx}=createHarness(()=>{},storage);
    const doc=fakeDocument();ctx.document=doc;
    const chooser=evaluate(ctx, "chooseAria2Directory('https://aria2.example.test/jsonrpc')");
    await waitForDialog(doc);
    const dialog=doc.body.children[0].children[0];
    assert.equal(dialog['role'],'dialog');
    const input=dialog.children[2].children[0];
    assert.equal(input.value,'');
    assert.equal(dialog.children.length,5,'no history selector on first use');
    const confirm=dialog.children[4].children[1];
    confirm.onclick();
    assert.equal(doc.body.children.length,1,'cannot submit an empty directory');
    assert.match(dialog.children[3].textContent,/请填写保存目录/);
    input.value=' /downloads/custom ';
    confirm.onclick();
    assert.equal(await chooser,'/downloads/custom');
    assert.equal(doc.body.children.length,0);
    assert.equal(doc.listeners.size,0);
    assert.equal(storage.size,1,'only existing aria2 client identifier may be stored');
    const cancelled=evaluate(ctx, "chooseAria2Directory('https://aria2.example.test/jsonrpc')");
    await waitForDialog(doc);
    const cancelDialog=doc.body.children[0].children[0];
    cancelDialog.children[4].children[0].onclick();
    assert.equal(await cancelled,null);
    assert.equal(doc.body.children.length,0);
    assert.equal(storage.has('ARIA2_DIR_HISTORY:https://aria2.example.test/jsonrpc'),false);
});

test('aria2 directory history remembers only successful RPC, most recent first, per endpoint', async () => {
    const storage=new Map();
    const {ctx}=createHarness(()=>{},storage);
    const calls=[];
    ctx.aria2Mock={
        rpc:'https://aria2.example.test/jsonrpc',
        addUri(uri, dir) {
            calls.push({uri,dir});
            return Promise.resolve('aria2-gid-'+calls.length);
        }
    };
    evaluate(ctx,'ariaClient = aria2Mock');
    await evaluate(ctx,"submitToAria2('https://site.test/a.torrent','/downloads/a')");
    await evaluate(ctx,"submitToAria2('https://site.test/b.torrent','/downloads/b')");
    await evaluate(ctx,"submitToAria2('https://site.test/c.torrent','/downloads/a')");
    const dirs=evaluate(ctx,"aria2DirectoryHistory(ariaClient.rpc)");
    assert.deepEqual(Array.from(dirs),['/downloads/a','/downloads/b']);
    assert.equal(calls[1].dir,'/downloads/b');
    ctx.aria2Mock.rpc='https://another-aria2.test/jsonrpc';
    assert.deepEqual(Array.from(evaluate(ctx,'aria2DirectoryHistory(ariaClient.rpc)')),[]);
    assert.deepEqual(Array.from(storage.get('ARIA2_DIR_HISTORY:https://aria2.example.test/jsonrpc')),['/downloads/a','/downloads/b']);
});

test('aria2 directory history is capped at 10, strips blanks and tolerates damaged storage', () => {
    const storage=new Map([['ARIA2_DIR_HISTORY:test', {not:'an array'}]]);
    const {ctx}=createHarness(()=>{},storage);
    assert.deepEqual(Array.from(evaluate(ctx,"aria2DirectoryHistory('test')")),[]);
    for(let i=0;i<15;i++)evaluate(ctx,"rememberAria2Directory('test','/dir/"+i+"')");
    assert.equal(storage.get('ARIA2_DIR_HISTORY:test').length,10);
    assert.equal(storage.get('ARIA2_DIR_HISTORY:test')[0],'/dir/14');
    assert.equal(storage.get('ARIA2_DIR_HISTORY:test')[9],'/dir/5');
    evaluate(ctx,"rememberAria2Directory('test','   /dir/9   ')");
    assert.equal(storage.get('ARIA2_DIR_HISTORY:test')[0],'/dir/9');
    evaluate(ctx,"rememberAria2Directory('test','   ')");
    assert.equal(storage.get('ARIA2_DIR_HISTORY:test').length,10);
});

test('aria2 dialog pre-fills last used directory, offers history and permits a new path', async () => {
    const storage=new Map([['ARIA2_DIR_HISTORY:rpc1',['/downloads/last','/downloads/old']]]);
    const {ctx}=createHarness(()=>{},storage);
    const doc=fakeDocument();ctx.document=doc;
    const chooser=evaluate(ctx,"chooseAria2Directory('rpc1')");
    await waitForDialog(doc);
    const dialog=doc.body.children[0].children[0];
    const input=dialog.children[2].children[0];
    const select=dialog.children[3].children[0];
    assert.equal(input.value,'/downloads/last');
    assert.equal(select.children.length,2);
    assert.equal(select.children[1].textContent,'/downloads/old');
    select.value='/downloads/old';
    select.onchange();
    assert.equal(input.value,'/downloads/old');
    input.value='/downloads/new';
    dialog.children[5].children[1].onclick();
    assert.equal(await chooser,'/downloads/new');
    assert.deepEqual(storage.get('ARIA2_DIR_HISTORY:rpc1'),['/downloads/last','/downloads/old'], 'choosing without accepted download should not modify history');
});

test('failed aria2 RPC preserves directory history and does not report success', async () => {
    const storage=new Map([['ARIA2_DIR_HISTORY:rpc1',['/downloads/last']]]);
    const {ctx}=createHarness(()=>{},storage);
    ctx.aria2Mock={rpc:'rpc1',addUri(){return Promise.reject(new Error('remote offline'));}};
    evaluate(ctx,'ariaClient = aria2Mock');
    await assert.rejects(evaluate(ctx,"submitToAria2('magnet:?xt=urn:btih:abcdef','/downloads/broken')"),/remote offline/);
    assert.deepEqual(storage.get('ARIA2_DIR_HISTORY:rpc1'),['/downloads/last']);
    ctx.aria2Mock.addUri=()=>Promise.resolve(null);
    await assert.rejects(evaluate(ctx,"submitToAria2('magnet:?xt=urn:btih:abcdef','/downloads/broken')"),/任务 ID/);
    assert.deepEqual(storage.get('ARIA2_DIR_HISTORY:rpc1'),['/downloads/last']);
});

test('torrent page aria2 button prompts for directory and preserves cancellation and success behavior', async () => {
    const {ctx,storage}=createHarness(()=>{});
    const doc=fakeDocument();ctx.document=doc;
    const calls=[];
    ctx.aria2Mock={rpc:'rpc1',addUri(uri,dir){calls.push({uri,dir});return Promise.resolve('gid-123');}};
    evaluate(ctx,'ariaClient = aria2Mock');
    const obj={
        gid:789,link:'https://e-hentai.org/torrent/789/abc.torrent',
        showLoading(){this.loading=true;},
        showMessage(msg){this.message=msg;}
    };
    ctx.testObj=obj;
    const pending=evaluate(ctx,'SendTaskButton.prototype.buttonClick.call(testObj)');
    await waitForDialog(doc);
    const dialog=doc.body.children[0].children[0];
    dialog.children[2].children[0].value='/downloads/torrent';
    dialog.children[4].children[1].onclick();
    await pending;
    assert.equal(calls.length,1);
    assert.equal(calls[0].dir,'/downloads/torrent');
    assert.equal(obj.message,'成功');
    assert.equal(storage.get('ARIA2_DIR_HISTORY:rpc1')[0],'/downloads/torrent');
    const canceled=evaluate(ctx,'SendTaskButton.prototype.buttonClick.call(testObj)');
    await waitForDialog(doc);
    doc.body.children[0].children[0].children[5].children[0].onclick();
    await canceled;
    assert.equal(calls.length,1,'no aria2 task should be created when selection is cancelled');
});

test('archive one-click prompts before requesting archive URL and cancellation costs no request', async () => {
    const {ctx}=createHarness(()=>{});
    const doc=fakeDocument();ctx.document=doc;
    ctx.aria2Mock={rpc:'rpc1',addUri(){throw new Error('should not be called');}};
    evaluate(ctx,'ariaClient = aria2Mock');
    let requestCount=0;
    ctx.fetch=()=>{requestCount++;throw new Error('should not fetch');};
    const btn=evaluate(ctx,"oneClickButton(123,'https://e-hentai.org/g/123/a/',null)");
    const click=btn.onclick();
    await waitForDialog(doc);
    doc.body.children[0].children[0].children[4].children[0].onclick();
    await click;
    assert.equal(requestCount,0);
    assert.equal(btn.textContent,'🡇');
});

test('aria2 popup download uses interactive directory instead of hidden ARIA2_DIR setting', () => {
    assert.doesNotMatch(script, /'ARIA2_DIR':\s*\{/);
    assert.doesNotMatch(script, /gmc\.get\('ARIA2_DIR'\)/);
    assert.match(script,/const ariaButton = event\.target\.closest && event\.target\.closest\('\.bt-download-button'\)/);
    assert.match(script,/await chooseAria2Directory\(ariaClient\.rpc\)/);
    assert.match(script,/const taskId = await submitToAria2\(getTorrentLink\(link\), dir\)/);
    assert.match(script,/const taskId = await submitToAria2\(downloadLink, dir\)/);
});

test('scissors copies SHA-1 of real torrent info bytes and never submits to Bridge', async () => {
    const sample = exampleTorrent();
    const {ctx, requests, clipboard, delayedUpdates, alerts} = createHarness(req =>
        req.onload({status:200,response:sample.arrayBuffer}));
    const button={textContent:'✂',title:'复制磁链',dataset:{}};
    ctx.copyButton=button;
    await evaluate(ctx, "copyTorrentMagnetToClipboard('https://e-hentai.org/torrent/123/abcdef.torrent', copyButton)");
    assert.equal(requests.length,1);
    assert.equal(requests[0].responseType,'arraybuffer');
    assert.equal(clipboard.length,1);
    assert.equal(clipboard[0].type,'text');
    assert.equal(clipboard[0].value,
        'magnet:?xt=urn:btih:' + sample.hash + '&tr=' + encodeURIComponent(sample.announce));
    assert.equal(button.textContent,'✔');
    assert.equal(button.title,'磁链已复制到剪贴板');
    assert.equal(alerts.length,0);
    assert.equal(delayedUpdates.length,1);
    delayedUpdates[0]();
    assert.equal(button.textContent,'✂');
    assert.equal(button.title,'复制磁链');
});

test('scissors may copy an already provided magnet without network access', async () => {
    const {ctx,requests,clipboard}=createHarness(() => {});
    const button={textContent:'✂',title:'复制磁链',dataset:{}};
    ctx.copyButton=button;
    await evaluate(ctx, "copyTorrentMagnetToClipboard('magnet:?xt=urn:btih:" + hexHash + "', copyButton)");
    assert.equal(requests.length,0);
    assert.equal(clipboard[0].value,'magnet:?xt=urn:btih:' + hexHash);
});

test('scissors shows an error and never copies invalid or inaccessible torrents', async () => {
    const situations=[
        [req=>req.onload({status:200,response:Uint8Array.from(Buffer.from('<html>login</html>')).buffer}),/种子文件|BitTorrent/],
        [req=>req.onload({status:403,response:null}),/HTTP 403/],
        [req=>req.ontimeout({}),/超时/]
    ];
    for(const [respond,pattern] of situations) {
        const {ctx,clipboard,alerts,delayedUpdates}=createHarness(respond);
        const button={textContent:'✂',title:'复制磁链',dataset:{}};
        ctx.copyButton=button;
        await evaluate(ctx, "copyTorrentMagnetToClipboard('https://e-hentai.org/torrent/123/abcdef.torrent', copyButton)");
        assert.equal(clipboard.length,0);
        assert.equal(alerts.length,1);
        assert.match(alerts[0],pattern);
        assert.equal(button.textContent,'✕');
        assert.equal(button.dataset.copyBusy,'0');
        delayedUpdates[0]();
        assert.equal(button.textContent,'✂');
    }
});

test('repeated scissors clicks while download is pending do not trigger multiple requests', async () => {
    let pending;
    const {ctx,requests,clipboard}=createHarness(req=>{pending=req;});
    const button={textContent:'✂',title:'复制磁链',dataset:{}};
    ctx.copyButton=button;
    const first=evaluate(ctx, "copyTorrentMagnetToClipboard('https://e-hentai.org/torrent/123/abcdef.torrent', copyButton)");
    const second=evaluate(ctx, "copyTorrentMagnetToClipboard('https://e-hentai.org/torrent/123/abcdef.torrent', copyButton)");
    assert.equal(requests.length,1);
    assert.equal(button.dataset.copyBusy,'1');
    pending.onload({status:200,response:exampleTorrent().arrayBuffer});
    await Promise.all([first,second]);
    assert.equal(clipboard.length,1);
});

test('torrent page and popup preserve aria2 and add separate Bridge entries', () => {
    assert.match(script, /this\.bridgeButton\.onclick = \(\) => sendTorrentToBridge\(this\.link, this\.bridgeButton\)/);
    assert.match(script, /const bridgeButton = event\.target\.closest/);
    assert.match(script, /ariaClient\.addUri\(getTorrentLink\(link\), gmc\.get\('ARIA2_DIR'\)\)/);
    assert.match(script, /class="aria2helper-one-click bt-bridge-button bt"/);
    assert.match(script, /@connect\s+\*/);
    assert.match(script, /event\.target\.closest\('.bt-copy-button'\)/);
    assert.match(script, /await copyTorrentMagnetToClipboard\(copyButton\.dataset\.link, copyButton\)/);
    assert.doesNotMatch(script, /event\.target\.parentNode\.contains\("bt-copy-button"\)/);
});
