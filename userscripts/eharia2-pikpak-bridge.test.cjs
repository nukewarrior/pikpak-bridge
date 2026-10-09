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


async function waitForBridgeTargets(doc) {
    await waitForDialog(doc);
    for(let i=0; i<100; i++) {
        const dialog=doc.body.children[0]?.children[0];
        const select=dialog?.children.find(child => child.tagName === 'SELECT');
        if(select && !select.disabled) return;
        await new Promise(resolve => setImmediate(resolve));
    }
    throw new Error('Bridge targets did not become selectable');
}

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
        dispatchPointer(event) { listeners.get('pointerdown')?.(event); },
        listeners
    };
    return doc;

    function element(tagName) {
        return {
            tagName: tagName.toUpperCase(),
            children: [],
            get firstElementChild() { return this.children[0] || null; },
            contains(node) { return node === this || this.children.some(child => child.contains(node)); },
            dataset: {},
            style: {},
            classList: {
                items: new Set(),
                add(...names) { names.forEach(name => this.items.add(name)); },
                remove(...names) { names.forEach(name => this.items.delete(name)); },
                contains(name) {return this.items.has(name);}
            },
            value: '',
            appendChild(child) {
                if(child.parentNode) {
                    child.parentNode.children = child.parentNode.children.filter(item => item !== child);
                }
                child.parentNode = this;
                this.children.push(child);
            },
            insertBefore(child, reference) {
                const index=this.children.indexOf(reference);
                if(index<0) throw new Error('Reference node is not a child');
                if(child.parentNode) {
                    child.parentNode.children = child.parentNode.children.filter(item => item !== child);
                }
                child.parentNode=this;
                const position=this.children.indexOf(reference);
                this.children.splice(position,0,child);
            },
            getAttribute(name) {return this[name] || null;},
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
    const doc=fakeDocument();ctx.document=doc;
    const button = {tagName:'INPUT',dataset:{},value:'发送到 PikPak',disabled:false,title:''};
    ctx.testButton = button;
    await evaluate(ctx,"sendTorrentToBridge('https://exhentai.org/torrent/123/" + hexHash + "/file.torrent', testButton)");
    assert.equal(requests.length,3);
    assert.equal(requests[0].method,'GET');
    assert.equal(requests[1].responseType,'arraybuffer');
    assert.equal(requests[2].method,'POST');
    assert.equal(JSON.parse(requests[2].data).target,'movies');
    assert.equal(storage.get('PIKPAK_BRIDGE_LAST_TARGET:https://bridge.example.test/base'), 'movies');
    assert.equal(button.value,'已提交');
    assert.equal(button.disabled,false);
    assert.equal(doc.body.children.length,0,'single target auto-selects and dismisses the loading dialog');
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
    await waitForBridgeTargets(doc);
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
    await waitForBridgeTargets(doc);
    const cancel=doc.body.children[0].children[0].children[3].children[0];
    cancel.onclick();
    await sending;
    assert.equal(requests.length,1,'cancel must skip Torrent download');
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
    await waitForBridgeTargets(doc);
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
        await waitForBridgeTargets(doc);
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
    assert.equal(dialog.children.length,5,'one input row, no separate history row');
    const field=dialog.children[2];
    assert.equal(field.children.length,3,'input, history icon and popup menu share one row');
    assert.equal(field.children[1].disabled,true,'clock is disabled before any download history');
    assert.equal(field.children[2].hidden,true);
    assert.match(field.children[1].innerHTML, /<svg/);
    assert.doesNotMatch(dialog.children.map(child=>child.textContent || '').join(' '), /最近使用/);
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

test('aria2 compact directory row opens clock history, fills selected path and still accepts new input', async () => {
    const storage=new Map([['ARIA2_DIR_HISTORY:rpc1',['/downloads/last','/downloads/old']]]);
    const {ctx}=createHarness(()=>{},storage);
    const doc=fakeDocument();ctx.document=doc;
    const chooser=evaluate(ctx,"chooseAria2Directory('rpc1')");
    await waitForDialog(doc);
    const dialog=doc.body.children[0].children[0];
    const field=dialog.children[2];
    const input=field.children[0];
    const clock=field.children[1];
    const menu=field.children[2];
    assert.equal(dialog.children.length,5,'compact layout has one input row');
    assert.equal(input.value,'/downloads/last');
    assert.equal(clock.disabled,false);
    assert.equal(clock['aria-label'],'选择历史保存目录');
    assert.match(clock.innerHTML, /<svg/);
    assert.equal(clock.textContent || '', '');
    assert.equal(menu.hidden,true);
    assert.equal(menu.children.length,2);
    assert.equal(menu.children[1].textContent,'/downloads/old');
    assert.equal(clock['aria-expanded'],'false');
    clock.onclick();
    assert.equal(menu.hidden,false);
    assert.equal(clock['aria-expanded'],'true');
    menu.children[1].onclick();
    assert.equal(input.value,'/downloads/old');
    assert.equal(menu.hidden,true);
    assert.equal(clock['aria-expanded'],'false');
    input.value='/downloads/new';
    dialog.children[4].children[1].onclick();
    assert.equal(await chooser,'/downloads/new');
    assert.equal(doc.listeners.size,0,'keyboard and pointer listeners are cleaned up');
    assert.deepEqual(storage.get('ARIA2_DIR_HISTORY:rpc1'),['/downloads/last','/downloads/old'], 'choosing without accepted download should not modify history');
});

test('aria2 history popup toggles, Escape closes menu first and outside click dismisses popup', async () => {
    const {ctx}=createHarness(()=>{},new Map([['ARIA2_DIR_HISTORY:rpc2',['/d1','/d2']]]));
    const doc=fakeDocument();ctx.document=doc;
    const chooser=evaluate(ctx,"chooseAria2Directory('rpc2')");
    await waitForDialog(doc);
    const dialog=doc.body.children[0].children[0];
    const field=dialog.children[2];
    const input=field.children[0];
    const clock=field.children[1];
    const menu=field.children[2];
    clock.onclick();
    assert.equal(menu.hidden,false);
    clock.onclick();
    assert.equal(menu.hidden,true);
    clock.onclick();
    doc.dispatchKey({key:'Escape',target:menu.children[0],preventDefault() {}});
    assert.equal(menu.hidden,true);
    assert.equal(doc.body.children.length,1,'Escape with history open must not cancel the directory dialog');
    clock.onclick();
    doc.dispatchPointer({target:input});
    assert.equal(menu.hidden,true);
    clock.onclick();
    doc.dispatchPointer({target:menu.children[1]});
    assert.equal(menu.hidden,false,'clicking a menu item must not be dismissed before selection');
    menu.children[0].onclick();
    assert.equal(input.value,'/d1');
    doc.dispatchKey({key:'Escape',target:input,preventDefault() {}});
    assert.equal(await chooser,null);
    assert.equal(doc.body.children.length,0);
    assert.equal(doc.listeners.size,0);
});

test('editing input closes aria2 history and never overwrites the typed path', async () => {
    const {ctx}=createHarness(()=>{},new Map([['ARIA2_DIR_HISTORY:rpc3',['/a','/b']]]));
    const doc=fakeDocument();ctx.document=doc;
    const chooser=evaluate(ctx,"chooseAria2Directory('rpc3')");
    await waitForDialog(doc);
    const dialog=doc.body.children[0].children[0];
    const field=dialog.children[2];
    const input=field.children[0];
    const clock=field.children[1];
    const menu=field.children[2];
    clock.onclick();
    input.value='/typed-new-path';
    input.oninput();
    assert.equal(menu.hidden,true);
    assert.equal(input.value,'/typed-new-path');
    dialog.children[4].children[1].onclick();
    assert.equal(await chooser,'/typed-new-path');
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
    const {ctx,storage,config}=createHarness(()=>{});
    config.ARIA2_RPC='https://aria2.example.test/jsonrpc';
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
    doc.body.children[0].children[0].children[4].children[0].onclick();
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
    assert.match(script,/await chooseAria2Directory\(ariaClient\.rpc, info\)/);
    assert.match(script,/const taskId = await submitToAria2\(getTorrentLink\(link\), dir\)/);
    assert.match(script,/const taskId = await submitToAria2\(downloadLink, dir\)/);
});


test('file card contains real torrent name and size, safe text, and leaves missing pages out', () => {
    const {ctx}=createHarness(()=>{});
    const doc=fakeDocument();ctx.document=doc;
    const malicious='<img src=x onerror=alert(1)> [Pixiv] Gallery.zip';
    const fileInfo={
        name:malicious,size:'428.1 MiB',pages:'',kind:'torrent'
    };
    ctx.fileInfo=fileInfo;
    const card=evaluate(ctx,'makeDownloadFileInfo(fileInfo)');
    assert.equal(card.className,'aria2helper-file-info');
    assert.equal(card['aria-label'],'待下载文件信息');
    assert.equal(card.children[0].textContent,'ZIP');
    assert.equal(card.children[1].children[0].textContent,malicious);
    assert.equal(card.children[1].children[0].title,malicious);
    assert.equal(card.children[1].children[1].children[0].textContent,'大小：428.1 MiB');
    assert.equal(card.children[1].children[1].children[1].textContent,'ZIP（按名称推断）');
    assert.equal(card.children[1].children[1].children.length,2);
    const noInfo=evaluate(ctx,"makeDownloadFileInfo({})");
    assert.equal(noInfo,null);
});

test('gallery info extracts title, page count, and size without inventing a ZIP length', () => {
    const {ctx}=createHarness(()=>{});
    const doc=fakeDocument();ctx.document=doc;
    const rows=[
        {querySelector(selector){return selector==='.gdt1'?{textContent:'Length:'}:{textContent:'10 pages'};}},
        {querySelector(selector){return selector==='.gdt1'?{textContent:'File Size:'}:{textContent:'428.1 MiB'};}}
    ];
    doc.querySelector=selector=>selector==='#gn'?{textContent:'Example Gallery'}
        : selector==='#gj'?{textContent:'Other Title'}:null;
    doc.querySelectorAll=selector=>selector==='#gdd tr'?rows:[];
    const info=evaluate(ctx,"archiveFileInfo()");
    assert.equal(info.name,'Example Gallery');
    assert.equal(info.size,'428.1 MiB');
    assert.equal(info.pages,'10 pages');
    assert.equal(info.kind,'archive');
    ctx.fileInfo=info;
    const card=evaluate(ctx,"makeDownloadFileInfo(fileInfo)");
    const values=card.children[1].children[1].children.map(child=>child.textContent);
    assert.deepEqual(values,[
        '画廊参考大小：428.1 MiB','ZIP 存档','页数：10 pages'
    ]);
    assert.equal(card.children[0].textContent,'ZIP');
    // On gallery list pages without full data, only the name and kind are shown.
    doc.querySelector=()=>null;
    doc.querySelectorAll=()=>[];
    const missing=evaluate(ctx,"archiveFileInfo('List Gallery')");
    ctx.fileInfo=missing;
    const fallback=evaluate(ctx,"makeDownloadFileInfo(fileInfo)");
    assert.equal(fallback.children[1].children[0].textContent,'List Gallery');
    assert.deepEqual(fallback.children[1].children[1].children.map(x=>x.textContent),['ZIP 存档']);
});

test('torrent item metadata uses corresponding gallery only and preserves exact popup values', () => {
    const {ctx}=createHarness(()=>{});
    const doc=fakeDocument();ctx.document=doc;
    doc.querySelector=selector=>selector==='#gn'?{textContent:'Gallery'}:null;
    doc.querySelectorAll=selector=>selector==='#gdd tr'?[{
        querySelector(x){return x==='.gdt1'?{textContent:'Length:'}:{textContent:'75 pages'};}
    }]:[];
    ctx.item={name:'Actual Torrent Pack.zip',size:'1.2 GiB'};
    let local=evaluate(ctx,'torrentFileInfo(item,123)');
    assert.equal(local.name,'Actual Torrent Pack.zip');
    assert.equal(local.size,'1.2 GiB');
    assert.equal(local.pages,'75 pages');
    let external=evaluate(ctx,'torrentFileInfo(item,456)');
    assert.equal(external.pages,'','gallery page count must not be assigned to another gallery in list');
});

test('torrent download page reads visible filename and optional size, without inventing missing fields', () => {
    const {ctx}=createHarness(()=>{});
    ctx.table={
        textContent:'Uploaded 2026-10-08 Size: 178.2 MiB Seeds: 9',
        querySelector(selector){return selector==='a'?{textContent:'Named Archive.cbz'}:null;}
    };
    const info=evaluate(ctx,'torrentPageFileInfo(table)');
    assert.equal(info.name,'Named Archive.cbz');
    assert.equal(info.size,'178.2 MiB');
    ctx.table.textContent='No reliable size';
    assert.equal(evaluate(ctx,'torrentPageFileInfo(table)').size,'');
});

test('aria2 modal shows metadata card between subtitle and input without disturbing directory history', async () => {
    const {ctx}=createHarness(()=>{});
    const doc=fakeDocument();ctx.document=doc;
    ctx.fileInfo={name:'Gallery Torrent.zip',size:'800 MiB',kind:'torrent'};
    const pending=evaluate(ctx,"chooseAria2Directory('rpc-card', fileInfo)");
    await waitForDialog(doc);
    const dialog=doc.body.children[0].children[0];
    assert.equal(dialog.children[2].className,'aria2helper-file-info');
    assert.equal(dialog.children[3].className,'aria2helper-aria2-dir-field');
    assert.equal(dialog.children[3].children[0].value,'');
    dialog.children[3].children[0].value='/newdir';
    dialog.children[5].children[1].onclick();
    assert.equal(await pending,'/newdir');
});

test('PikPak modal shows real torrent metadata and target selection remains functional', async () => {
    const {ctx}=createHarness(()=>{});
    const doc=fakeDocument();ctx.document=doc;
    ctx.targets=[{id:'a',name:'Alpha',default:true},{id:'b',name:'Beta'}];
    ctx.fileInfo={name:'Album.zip',size:'320 MiB',pages:'9 pages',kind:'torrent'};
    const pending=evaluate(ctx,"chooseBridgeTarget(targets,'https://bridge.test',fileInfo)");
    await waitForDialog(doc);
    const dialog=doc.body.children[0].children[0];
    assert.equal(dialog.children[2].className,'aria2helper-file-info');
    assert.equal(dialog.children[2].children[1].children[0].textContent,'Album.zip');
    assert.equal(dialog.children[3].value,'a');
    dialog.children[3].value='b';
    dialog.children[4].children[1].onclick();
    const selection=await pending;
    assert.equal(selection.id,'b');
    assert.equal(doc.body.children.length,0);
});


test('archive list: extended layout reads visible title even when cover link has no text', () => {
    const {ctx}=createHarness(()=>{});
    ctx.row={
        textContent:'Manga 2026-10-08 77 pages uploader Example Gallery Title',
        querySelector(selector) {
            if(selector === '.glink') return {textContent:'[Anthology] Example Gallery [Chinese]'};
            return null;
        }
    };
    ctx.cover={href:'https://e-hentai.org/g/123/abc/',textContent:''};
    const info=evaluate(ctx,'galleryListArchiveFileInfo(row)');
    assert.equal(info.name,'[Anthology] Example Gallery [Chinese]');
    assert.equal(info.pages,'77 pages');
    assert.equal(info.size,'', 'listing without explicit File Size must not invent ZIP size');
    assert.equal(info.kind,'archive');
    assert.equal(ctx.cover.textContent,'','cover may lack text; title must come from row');
    ctx.info=info;
    const doc=fakeDocument();ctx.document=doc;
    const card=evaluate(ctx,'makeDownloadFileInfo(info)');
    assert.equal(card.children[1].children[0].textContent,'[Anthology] Example Gallery [Chinese]');
    assert.deepEqual(card.children[1].children[1].children.map(x=>x.textContent),
        ['ZIP 存档','页数：77 pages']);
});

test('archive list: compact, minimal, and thumbnail layouts use title nodes in their own gallery row', () => {
    const samples=[
        {layout:'compact',selector:'.glink',title:'Compact Gallery'},
        {layout:'minimal',selector:'.glink',title:'Minimal Gallery'},
        {layout:'thumbnail',selector:'.gl4t.glname',title:'Thumbnail Gallery'}
    ];
    const {ctx}=createHarness(()=>{});
    for(const sample of samples) {
        ctx.row={
            textContent:sample.title+' 12 pages',
            querySelector(selector) {
                if(selector === sample.selector) return {textContent:sample.title};
                return null;
            }
        };
        const info=evaluate(ctx,'galleryListArchiveFileInfo(row)');
        assert.equal(info.name,sample.title,sample.layout);
        assert.equal(info.pages,'12 pages',sample.layout);
        assert.equal(info.size,'',sample.layout);
    }
});

test('archive list: missing title, explicit size and row isolation are handled independently', () => {
    const {ctx}=createHarness(()=>{});
    const values={
        '.glink': {textContent:'   '},
        '.glname a': {textContent:'Fallback title'}
    };
    ctx.row={
        textContent:'This gallery File Size: 428.1 MiB 9 pages',
        querySelector(selector) { return values[selector] || null; }
    };
    let info=evaluate(ctx,'galleryListArchiveFileInfo(row)');
    assert.equal(info.name,'Fallback title');
    assert.equal(info.size,'428.1 MiB');
    assert.equal(info.pages,'9 pages');

    ctx.row={
        textContent:'No metadata to extract',
        querySelector(){ return null; }
    };
    info=evaluate(ctx,'galleryListArchiveFileInfo(row)');
    assert.equal(info.name,'');
    assert.equal(info.size,'');
    assert.equal(info.pages,'');
    ctx.row=null;
    assert.equal(evaluate(ctx,'galleryListArchiveFileInfo(row)').name,'');
    assert.equal(evaluate(ctx,'makeDownloadFileInfo(galleryListArchiveFileInfo(row))'),null);
});

test('archive one-click: list metadata appears in aria2 modal without requesting paid URL before confirmation', async () => {
    const {ctx}=createHarness(()=>{});
    const doc=fakeDocument();ctx.document=doc;
    ctx.aria2Mock={rpc:'rpc1',addUri(){throw new Error('must not submit')}}
    evaluate(ctx,'ariaClient = aria2Mock');
    let requestCount=0;
    ctx.fetch=()=>{requestCount++;throw new Error('must not fetch paid archive')};
    ctx.row={
        textContent:'Gallery 77 pages',
        querySelector(selector) {
            return selector === '.glink' ? {textContent:'True Gallery Title'} : null;
        }
    };
    const info=evaluate(ctx,'galleryListArchiveFileInfo(row)');
    ctx.info=info;
    const button=evaluate(ctx,"oneClickButton(123,'https://e-hentai.org/g/123/abc/',null,info)");
    const pending=button.onclick();
    await waitForDialog(doc);
    const dialog=doc.body.children[0].children[0];
    assert.equal(dialog.children[2].className,'aria2helper-file-info');
    assert.equal(dialog.children[2].children[1].children[0].textContent,'True Gallery Title');
    assert.deepEqual(dialog.children[2].children[1].children[1].children.map(x=>x.textContent),
        ['ZIP 存档','页数：77 pages']);
    assert.equal(dialog.children[3].className,'aria2helper-aria2-dir-field');
    dialog.children[5].children[0].onclick();
    await pending;
    assert.equal(requestCount,0);
    assert.equal(doc.body.children.length,0);
});

test('archive one-click: gallery detail path keeps original metadata source and no extra requests', () => {
    assert.match(script,/oneClickButton\(GID, null, archiverLink, archiveFileInfo\(\)\)/);
    assert.match(script,/const fileInfo = galleryListArchiveFileInfo\(tr\)/);
    assert.match(script,/oneClickButton\(gid, link, null, fileInfo\)/);
    assert.doesNotMatch(script,/oneClickButton\(gid, link, null, archiveFileInfo\(a\.textContent\)\)/);
});

test('archive download confirmation shows only provided metadata and cancellation avoids fee request', async () => {
    const {ctx}=createHarness(()=>{});
    const doc=fakeDocument();ctx.document=doc;
    ctx.aria2Mock={rpc:'rpc1',addUri(){throw new Error('should not submit');}};
    evaluate(ctx,'ariaClient = aria2Mock');
    let remoteCalls=0;ctx.fetch=()=>{remoteCalls++;throw new Error('should not fetch')};
    ctx.fileInfo={name:'Gallery Title',pages:'10 pages',size:'428 MiB',kind:'archive'};
    const btn=evaluate(ctx,"oneClickButton(123,'https://e-hentai.org/g/123/a/',null,fileInfo)");
    const pending=btn.onclick();
    await waitForDialog(doc);
    const dialog=doc.body.children[0].children[0];
    assert.equal(dialog.children[2].children[1].children[0].textContent,'Gallery Title');
    assert.equal(dialog.children[2].children[1].children[1].children[0].textContent,'画廊参考大小：428 MiB');
    dialog.children[5].children[0].onclick();
    await pending;
    assert.equal(remoteCalls,0);
});

test('download metadata is threaded through every torrent and gallery action', () => {
    assert.match(script,/torents\.find\(item => item\.link === bridgeButton\.dataset\.link\)/);
    assert.match(script,/torents\.find\(item => item\.link === ariaButton\.dataset\.link\)/);
    assert.match(script,/sendTorrentToBridge\(bridgeButton\.dataset\.link, bridgeButton, info\)/);
    assert.match(script,/chooseAria2Directory\(ariaClient\.rpc, info\)/);
    assert.match(script,/new SendTaskButton\(GID, link, torrentPageFileInfo\(table\)\)/);
    assert.match(script,/const fileInfo = galleryListArchiveFileInfo\(tr\)/);
    assert.match(script,/oneClickButton\(gid, link, null, fileInfo\)/);
    assert.match(script,/oneClickButton\(GID, null, archiverLink, archiveFileInfo\(\)\)/);
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



test('native torrent page buttons are classified in English and Chinese', () => {
    const {ctx}=createHarness(()=>{});
    const doc=fakeDocument();ctx.document=doc;
    for(const [label,expected] of [
        ['Copy Magnet Link','copy'],
        ['复制磁力链','copy'],
        ['复制磁力链接','copy'],
        ['Information','info'],
        ['详细信息','info'],
        ['无关操作','']
    ]) {
        const node=doc.createElement('input');
        node.value=label;
        ctx.testControl=node;
        assert.equal(evaluate(ctx,'torrentPageNativeActionType(testControl)'),expected,label);
    }
    ctx.testControl=doc.createElement('div');
    ctx.testControl.textContent='Information is a description, not a control';
    assert.equal(evaluate(ctx,'torrentPageNativeActionType(testControl)'),'');
});

test('torrent page grid keeps the original native controls and form semantics', () => {
    const {ctx}=createHarness(()=>{});
    const doc=fakeDocument();ctx.document=doc;
    const form=doc.createElement('form');
    const host=doc.createElement('td');
    form.appendChild(host);
    const nativeCopy=doc.createElement('input');
    nativeCopy.type='submit';
    nativeCopy.name='copy_magnet';
    nativeCopy.value='Copy Magnet Link';
    let copyCount=0;
    nativeCopy.onclick=()=>{copyCount++;};
    const nativeInfo=doc.createElement('button');
    nativeInfo.type='submit';
    nativeInfo.name='torrent_info';
    nativeInfo.textContent='Information';
    let infoCount=0;
    nativeInfo.onclick=()=>{infoCount++;};
    const note=doc.createElement('div');
    note.textContent='Downloads: 96';
    ctx.widget=evaluate(ctx,"new SendTaskButton(123, 'https://e-hentai.org/torrent/example.torrent')");
    const widget=ctx.widget;
    host.appendChild(note);
    host.appendChild(widget.element);
    host.appendChild(nativeCopy);
    host.appendChild(nativeInfo);
    ctx.insertionPoint=nativeCopy;
    const ok=evaluate(ctx,'arrangeTorrentPageActions(insertionPoint, widget)');
    assert.equal(ok,true);
    assert.equal(host.children.length,2,'other torrent metadata remains in the original table cell');
    assert.equal(host.children[0],note);
    const grid=host.children[1];
    assert.equal(grid.className,'aria2helper-torrent-actions-grid');
    assert.deepEqual(grid.children,[widget.element,nativeCopy,nativeInfo]);
    assert.equal(grid.parentNode,host);
    assert.equal(host.classList.contains('aria2helper-torrent-actions-cell'),true,
        'the actual table cell must reserve enough width for two full-size buttons');
    assert.equal(form.children[0],host);
    assert.equal(nativeCopy.type,'submit');
    assert.equal(nativeInfo.type,'submit');
    assert.equal(nativeCopy.name,'copy_magnet');
    assert.equal(nativeInfo.name,'torrent_info');
    nativeCopy.onclick();nativeInfo.onclick();
    assert.equal(copyCount,1);
    assert.equal(infoCount,1);
    assert.equal(nativeCopy.classList.contains('aria2helper-native-copy'),true);
    assert.equal(nativeInfo.classList.contains('aria2helper-native-info'),true);
    assert.equal(widget.button.value,'Aria2');
    assert.equal(widget.bridgeButton.value,'PikPak');
    assert.equal(widget.button.title,'发送到 aria2');
    assert.equal(widget.bridgeButton.title,'发送到 PikPak Bridge');
    assert.equal(evaluate(ctx,'arrangeTorrentPageActions(insertionPoint, widget)'),false);
    assert.deepEqual(grid.children,[widget.element,nativeCopy,nativeInfo],'repeat activation does not rearrange nodes');
});

test('torrent grid reserves width through a wrapper and never changes unrelated gallery cells', () => {
    const {ctx}=createHarness(()=>{});
    const doc=fakeDocument();ctx.document=doc;
    const outer=doc.createElement('table');
    const row=doc.createElement('tr');
    const titleCell=doc.createElement('td');
    const actionCell=doc.createElement('td');
    const inner=doc.createElement('div');
    outer.appendChild(row);
    row.appendChild(titleCell);
    row.appendChild(actionCell);
    actionCell.appendChild(inner);
    const widget=evaluate(ctx,"new SendTaskButton(1,'https://e-hentai.org/torrent/example.torrent')");
    ctx.widget=widget;
    const nativeCopy=doc.createElement('input');nativeCopy.value='Copy Magnet Link';
    const nativeInfo=doc.createElement('input');nativeInfo.value='Information';
    inner.appendChild(widget.element);
    inner.appendChild(nativeCopy);
    inner.appendChild(nativeInfo);
    ctx.insertionPoint=nativeCopy;
    assert.equal(evaluate(ctx,'arrangeTorrentPageActions(insertionPoint,widget)'),true);
    assert.equal(actionCell.classList.contains('aria2helper-torrent-actions-cell'),true);
    assert.equal(titleCell.classList.contains('aria2helper-torrent-actions-cell'),false);
    assert.equal(inner.children[0].className,'aria2helper-torrent-actions-grid');
    assert.equal(inner.children[0].children.length,3);
});

test('torrent page leaves original controls untouched if the required native buttons cannot be identified', () => {
    const {ctx}=createHarness(()=>{});
    const doc=fakeDocument();ctx.document=doc;
    const host=doc.createElement('td');
    const widget=evaluate(ctx,"new SendTaskButton(123, 'https://e-hentai.org/torrent/example.torrent')");
    ctx.widget=widget;
    const copy=doc.createElement('input');copy.value='Copy Magnet Link';
    const unrecognized=doc.createElement('input');unrecognized.value='Report issue';
    host.appendChild(widget.element);
    host.appendChild(copy);
    host.appendChild(unrecognized);
    ctx.insertionPoint=copy;
    assert.equal(evaluate(ctx,'arrangeTorrentPageActions(insertionPoint, widget)'),false);
    assert.deepEqual(host.children,[widget.element,copy,unrecognized]);
    assert.equal(widget.button.value,'发送到Aria2');
    assert.equal(widget.bridgeButton.value,'发送到 PikPak');
    assert.equal(copy.title,undefined);
});

test('torrent page action grid is scoped, two rows by two columns, and preserves handlers', () => {
    assert.match(script, /#torrentinfo \.aria2helper-torrent-actions-grid\s*\{[^}]*grid-template-columns:\s*repeat\(2,\s*minmax\(0,\s*1fr\)\);[^}]*grid-template-rows:\s*38px 38px;[^}]*gap:\s*8px;[^}]*width:\s*232px;[^}]*min-width:\s*232px;/s);
    assert.match(script, /#torrentinfo td\.aria2helper-torrent-actions-cell\s*\{[^}]*width:\s*248px;[^}]*min-width:\s*248px;/s);
    assert.match(script, /#torrentinfo \.aria2helper-torrent-actions-grid > \.aria2helper-box > \.aria2helper-button[\s\S]*?white-space:\s*nowrap;/);
    assert.doesNotMatch(script, /width:\s*min\(232px,\s*100%\)/);
    assert.match(script, /#torrentinfo \.aria2helper-torrent-actions-grid > \.aria2helper-box\s*\{[^}]*grid-column:\s*1 \/ -1;/s);
    assert.match(script, /\.aria2helper-native-action\s*\{[^}]*grid-row:\s*2;/s);
    assert.match(script, /\.aria2helper-native-copy\s*\{\s*grid-column:\s*1;/s);
    assert.match(script, /\.aria2helper-native-info\s*\{\s*grid-column:\s*2;/s);
    assert.match(script,/arrangeTorrentPageActions\(insertionPoint, button\);/);
    assert.doesNotMatch(script, /function arrangeTorrentPageActions[\s\S]*nativeCopy\.onclick\s*=/);
});

test('torrent buttons have consistent aria2 / copy / PikPak order, accessible titles, and one shared cell', () => {
    const {ctx}=createHarness(()=>{});
    ctx.item={link:'https://e-hentai.org/torrent/55/item.torrent'};
    const html=evaluate(ctx,"torrentActionsCell(item, 55)");
    assert.equal((html.match(/<td\b/g)||[]).length,1);
    assert.equal((html.match(/bt-actions-group/g)||[]).length,1);
    assert.equal((html.match(/data-link=/g)||[]).length,3);
    assert.equal((html.match(/data-gid="55"/g)||[]).length,3);
    const classes=['bt-download-button','bt-copy-button','bt-bridge-button'];
    const indices=classes.map(name=>html.indexOf('class="aria2helper-one-click '+name));
    // The scissors button retains its extra "icon" class.
    assert.ok(indices.every(n=>n>=0));
    assert.ok(indices[0]<indices[1] && indices[1]<indices[2]);
    assert.match(html,/bt-download-button bt" title="发送到 aria2" aria-label="发送到 aria2"/);
    assert.match(html,/bt-copy-button icon bt" title="复制磁链" aria-label="复制磁链"/);
    assert.match(html,/bt-bridge-button bt" title="发送到 PikPak Bridge" aria-label="发送到 PikPak Bridge"/);
    assert.match(html,/role="group" aria-label="种子操作"/);
});

test('torrent popup left, right, and two-line tables each render three actions in one cell', () => {
    const {ctx}=createHarness(()=>{});
    ctx.item={
        link:'https://e-hentai.org/torrent/55/item.torrent',
        name:'name.torrent',
        size:'123 MiB',
        time:new Date('2026-10-08T10:00:00Z'),
        readableTime:'1小时前',seeds:3,peers:2,downloads:1
    };
    for(const [left,twoLines] of [[true,false],[false,false],[true,true],[false,true]]) {
        const html=evaluate(ctx, "torrentListRow(item, 55, "+left+", "+twoLines+", () => '')");
        assert.equal((html.match(/bt-actions-group/g)||[]).length,1,
            'one button group is rendered per row');
        assert.equal((html.match(/data-gid="55"/g)||[]).length,3);
        assert.equal((html.match(/<td\b/g)||[]).length,7,
            'three actions use one table cell instead of three');
        const groupPosition=html.indexOf('bt-actions-group');
        const namePosition=html.indexOf('bt-name');
        if(twoLines) {
            assert.match(html,/colspan="6"/);
            assert.equal((html.match(/<tr\b/g)||[]).length,2);
            assert.ok(namePosition<groupPosition,'two-line layout keeps name on first row');
        } else {
            assert.equal((html.match(/<tr\b/g)||[]).length,1);
            assert.equal(groupPosition<namePosition,left,'action group position matches side');
        }
    }
});

test('torrent action CSS fixes 8px spacing, circle size and icon centering', () => {
    assert.match(script, /#btList \.bt-actions-group\s*\{[^}]*display:\s*inline-flex;[^}]*align-items:\s*center;[^}]*gap:\s*8px;/s);
    assert.match(script, /#btList \.bt-actions-group \.aria2helper-one-click\s*\{[^}]*display:\s*inline-flex;[^}]*justify-content:\s*center;[^}]*width:\s*18px;[^}]*height:\s*18px;/s);
    assert.match(script, /#btList tr td\.bt-actions-cell\s*\{[^}]*padding:\s*2px 8px;/s);
    const groupOverride=script.indexOf('#btList tr td.bt-actions-cell');
    const oldFirstColumnRule=script.indexOf('#btList tr>td:first-of-type');
    assert.ok(groupOverride>oldFirstColumnRule,
        'new table cell padding must override first-column style');
    assert.match(script, /\$\{buttonLeft \? "<th><\/th>" : ""\}/);
    assert.match(script, /\$\{buttonLeft \? "" : "<th><\/th>"\}/);
    assert.doesNotMatch(script,/const button1 = \`<td class="bt-button/);
});

test('torrent page and popup preserve aria2 and add separate Bridge entries', () => {
    assert.match(script, /this\.bridgeButton\.onclick = \(\) => sendTorrentToBridge\(this\.link, this\.bridgeButton, this\.fileInfo\)/);
    assert.match(script, /const bridgeButton = event\.target\.closest/);
    assert.match(script, /submitToAria2\(getTorrentLink\(link\), dir\)/);
    assert.match(script, /class="aria2helper-one-click bt-bridge-button bt"/);
    assert.match(script, /@connect\s+\*/);
    assert.match(script, /event\.target\.closest\('.bt-copy-button'\)/);
    assert.match(script, /await copyTorrentMagnetToClipboard\(copyButton\.dataset\.link, copyButton\)/);
    assert.doesNotMatch(script, /event\.target\.parentNode\.contains\("bt-copy-button"\)/);
});


test('torrent page native buttons stretch to equal widths and keep vivid hover text', () => {
    assert.match(script, /#torrentinfo \.aria2helper-torrent-actions-grid > \.aria2helper-native-action\s*\{[^}]*justify-self:\s*stretch\s*!important;[^}]*width:\s*100%\s*!important;[^}]*max-width:\s*none\s*!important;/s);
    assert.match(script, /\.aria2helper-button:hover,[\s\S]*?background:\s*#3c7022\s*!important;[^}]*color:\s*#fff\s*!important;/);
    assert.match(script, /\.aria2helper-bridge-button:hover,[\s\S]*?background:\s*#11756e\s*!important;[^}]*color:\s*#fff\s*!important;/);
    assert.doesNotMatch(script, /widget\.button\.value\s*=\s*['"]↓\s*aria2/);
});

test('PikPak shows loading dialog before network completes and defers Torrent until confirmation', async () => {
    let pendingTargets;
    const {ctx,requests}=createHarness(req => {
        if(req.url.endsWith('/api/v1/targets')) {
            pendingTargets=req;
        } else if(req.responseType==='arraybuffer') {
            req.onload({status:200,response:exampleTorrent().arrayBuffer});
        } else {
            req.onload({status:201,responseText:'{"id":"task-confirmed"}'});
        }
    });
    const doc=fakeDocument();ctx.document=doc;
    const button={tagName:'INPUT',dataset:{},value:'发送到 PikPak',disabled:false,title:''};
    ctx.testButton=button;
    const sending=evaluate(ctx,"sendTorrentToBridge('https://exhentai.org/torrent/123/" + hexHash + "/file.torrent', testButton)");
    assert.equal(doc.body.children.length,1,'show modal synchronously, without waiting for GET');
    const dialog=doc.body.children[0].children[0];
    assert.match(dialog.children[1].textContent,/正在获取/);
    assert.equal(dialog.children[2].hidden,true);
    assert.equal(dialog.children[3].children[1].disabled,true);
    assert.equal(requests.length,1,'no Torrent download while loading target list');
    assert.equal(requests[0].method,'GET');
    pendingTargets.onload({status:200,responseText:JSON.stringify({
        targets:[{id:'first',name:'First'},{id:'second',name:'Second'}]
    })});
    await waitForBridgeTargets(doc);
    assert.equal(dialog.children[2].hidden,false);
    assert.equal(dialog.children[3].children[1].disabled,false);
    assert.equal(requests.length,1,'rendering target list must not fetch Torrent');
    dialog.children[3].children[1].onclick();
    await sending;
    assert.equal(requests.length,3);
    assert.equal(requests[1].responseType,'arraybuffer');
    assert.equal(requests[2].method,'POST');
    assert.equal(doc.body.children.length,0);
});

test('cancel during PikPak target loading ignores late responses without fetching Torrent', async () => {
    let pendingTargets;
    const {ctx,requests,alerts}=createHarness(req => {pendingTargets=req;});
    const doc=fakeDocument();ctx.document=doc;
    const button={tagName:'INPUT',dataset:{},value:'发送到 PikPak',disabled:false,title:''};
    ctx.testButton=button;
    const sending=evaluate(ctx,"sendTorrentToBridge('https://exhentai.org/torrent/123/" + hexHash + "/file.torrent', testButton)");
    assert.equal(doc.body.children.length,1);
    doc.body.children[0].children[0].children[3].children[0].onclick();
    await sending;
    assert.equal(doc.body.children.length,0);
    assert.equal(doc.listeners.size,0);
    assert.equal(requests.length,1);
    assert.equal(button.disabled,false);
    pendingTargets.onload({status:200,responseText:'{"targets":[{"id":"one"}]}'});
    await Promise.resolve();
    assert.equal(doc.body.children.length,0);
    assert.equal(requests.length,1);
    assert.equal(alerts.length,0);
});

test('Bridge target request failure closes loading dialog and does not fetch Torrent', async () => {
    const {ctx,requests,alerts}=createHarness(req => req.ontimeout({}));
    const doc=fakeDocument();ctx.document=doc;
    const button={tagName:'INPUT',dataset:{},value:'发送到 PikPak',disabled:false,title:''};
    ctx.testButton=button;
    await evaluate(ctx,"sendTorrentToBridge('https://exhentai.org/torrent/123/" + hexHash + "/file.torrent', testButton)");
    assert.equal(requests.length,1);
    assert.equal(doc.body.children.length,0);
    assert.equal(doc.listeners.size,0);
    assert.match(alerts[0],/超时/);
});


test('Bridge reports separate cloud and aria2 phase progress without mislabeling overall completion', () => {
    const {ctx}=createHarness(()=>{});
    ctx.t={status:'PIKPAK_RUNNING',pikpak_progress:64};
    let p=evaluate(ctx,'bridgeProgressOf(t)');
    assert.equal(p.label,'PikPak 云下载中 64%');
    assert.equal(p.percent,64);
    ctx.t={status:'ARIA2_DOWNLOADING',pikpak_progress:100};
    ctx.downloads=[
        {total_length:100,completed_length:20},
        {expected_size:300,completed_length:180}
    ];
    p=evaluate(ctx,'bridgeProgressOf(t,downloads)');
    assert.equal(p.percent,50);
    assert.equal(p.label,'Aria2 下载中 50%');
    ctx.t={status:'VERIFYING',pikpak_progress:100};
    p=evaluate(ctx,'bridgeProgressOf(t,downloads)');
    assert.equal(p.label,'校验中 50%');
    ctx.t={status:'WAITING_ARIA2',pikpak_progress:100};
    p=evaluate(ctx,'bridgeProgressOf(t,[])');
    assert.equal(p.percent,null,'no false 100% overall progress while waiting for aria2');
    ctx.t={status:'PIKPAK_FAILED',error:'云下载失败 <script>untrusted</script>'};
    p=evaluate(ctx,'bridgeProgressOf(t)');
    assert.equal(p.state,'failed');
    assert.match(p.error,/untrusted/);
    assert.equal(p.percent,null);
    ctx.t={status:'COMPLETED'};
    p=evaluate(ctx,'bridgeProgressOf(t)');
    assert.equal(p.label,'已完成 100%');
});

test('Bridge tracked torrents in the same gallery remain independent and restore on reload', async () => {
    const urlA='https://e-hentai.org/torrent/123/first.torrent';
    const urlB='https://e-hentai.org/torrent/123/second.torrent';
    const key='PIKPAK_BRIDGE_TRACKED_TASKS:https://bridge.example.test/base';
    const storage=new Map();
    const handler=req=>{
        if(req.url.includes('/downloads')) {
            assert.match(req.url,/task-b\/downloads/);
            req.onload({status:200,responseText:JSON.stringify({downloads:[
                {total_length:100,completed_length:20},
                {total_length:100,completed_length:80}
            ]})});
        } else if(req.url.includes('/api/v1/tasks?')) {
            req.onload({status:200,responseText:JSON.stringify({tasks:[
                {id:'task-a',status:'PIKPAK_RUNNING',pikpak_progress:64},
                {id:'task-b',status:'ARIA2_DOWNLOADING',pikpak_progress:100}
            ]})});
        } else throw new Error('unexpected request ' + req.url);
    };
    const {ctx,requests}=createHarness(handler,storage);
    const doc=fakeDocument();ctx.document=doc;
    evaluate(ctx,'bridgeProgressMonitor.schedule = () => {}');
    ctx.urlA=urlA;ctx.urlB=urlB;
    evaluate(ctx,"bridgeProgressMonitor.remember(urlA,'task-a',123,{name:'first.torrent'})");
    evaluate(ctx,"bridgeProgressMonitor.remember(urlB,'task-b',123,{name:'second.torrent'})");
    assert.equal(storage.get(key).length,2);
    ctx.first=evaluate(ctx,'bridgeProgressMonitor.watchTorrent(urlA)');
    ctx.second=evaluate(ctx,'bridgeProgressMonitor.watchTorrent(urlB)');
    ctx.summary=evaluate(ctx,'bridgeProgressMonitor.watchGallery(123)');
    await evaluate(ctx,'bridgeProgressMonitor.poll()');
    assert.equal(ctx.first.children[0].textContent,'PikPak · PikPak 云下载中 64%');
    assert.equal(ctx.second.children[0].textContent,'PikPak · Aria2 下载中 50%');
    assert.match(ctx.summary.children[0].textContent,/PikPak \(2项\).*Aria2 下载中 50%/);
    assert.equal(ctx.second.children[1].children[0].style.width,'50.00%');
    assert.equal(requests.length,2,'one batch status request and only the active aria2 file request');

    // Re-submitting one torrent (including a duplicate 409 task) replaces only that URL.
    evaluate(ctx,"bridgeProgressMonitor.remember(urlA,'task-a',123)");
    assert.equal(storage.get(key).length,2);

    const reloaded=createHarness(handler,storage);
    reloaded.ctx.document=fakeDocument();
    reloaded.ctx.urlA=urlA;
    evaluate(reloaded.ctx,'bridgeProgressMonitor.schedule = () => {}');
    const again=evaluate(reloaded.ctx,'bridgeProgressMonitor.watchTorrent(urlA)');
    assert.match(again.children[0].textContent,/查询任务中/);
    await evaluate(reloaded.ctx,'bridgeProgressMonitor.poll()');
    assert.equal(again.children[0].textContent,'PikPak · PikPak 云下载中 64%');
});

test('Bridge progress stops polling after terminal result and displays server errors safely', async () => {
    const requests=[];
    const {ctx}=createHarness(req => {
        requests.push(req);
        req.onload({status:200,responseText:JSON.stringify({
            tasks:[{id:'task-failed',status:'ARIA2_FAILED',
                error:'磁盘空间不足 <img src=x onerror=alert(1)>'}]
        })});
    });
    ctx.document=fakeDocument();
    evaluate(ctx,'bridgeProgressMonitor.schedule = () => {}');
    ctx.link='https://e-hentai.org/torrent/123/error.torrent';
    evaluate(ctx,"bridgeProgressMonitor.remember(link,'task-failed',123)");
    const view=evaluate(ctx,'bridgeProgressMonitor.watchTorrent(link)');
    await evaluate(ctx,'bridgeProgressMonitor.poll()');
    assert.equal(view.dataset.state,'failed');
    assert.match(view.title,/磁盘空间不足/);
    assert.match(view.children[0].textContent,/PikPak · Aria2 失败.*磁盘空间不足/);
    assert.equal(view.children[1].style.display,'none');
    assert.equal(requests.length,1);
});

test('Bridge progress entry points do not replace direct aria2 polling or torrent actions', () => {
    assert.match(script,/bridgeProgressMonitor\.remember\(torrentLink, task\.id, button\.dataset\.gid \|\| GID, fileInfo\)/);
    assert.match(script,/bridgeProgressMonitor\.watchTorrent\(link\)/);
    assert.match(script,/bridgeProgressMonitor\.watchTorrent\(item\.link\)/);
    assert.match(script,/bridgeProgressMonitor\.watchGallery\(GID\)/);
    assert.match(script,/bridgeProgressMonitor\.watchGallery\(gid\)/);
    assert.match(script,/const batch = await ariaClient\.batchTellStatus\(this\.taskIds\)/);
    assert.match(script,/return this\.getJSON\('\/api\/v1\/tasks\?limit=200'\)/);
    assert.match(script,/return this\.getJSON\('\/api\/v1\/tasks\/' \+ encodeURIComponent\(id\) \+ '\/downloads'\)/);
});

test('completed Bridge task asks before creating a forced replacement run', async () => {
    const prompts = [];
    const {ctx, requests} = createHarness(req => respondWithTorrent(req, request => {
        if(request.method === 'GET') {
            request.onload({status: 200, responseText: JSON.stringify({targets:[{id:'nas',name:'NAS'}]})});
        } else {
            const body=JSON.parse(request.data);
            request.onload(body.force
                ? {status:201,responseText:'{"id":"new-task","status":"PIKPAK_COMPLETE"}'}
                : {status:409,responseText:'{"existing_task_id":"old-task","status":"COMPLETED","target_id":"nas"}'});
        }
    }));
    ctx.window.confirm = message => {prompts.push(message);return true;};
    const doc=fakeDocument();ctx.document=doc;
    const button={tagName:'INPUT',dataset:{},value:'发送到 PikPak',disabled:false,title:''};
    ctx.testButton=button;
    await evaluate(ctx,"sendTorrentToBridge('https://exhentai.org/torrent/123/" + hexHash + "/file.torrent', testButton)");
    const posts=requests.filter(x=>x.method==='POST');
    assert.equal(posts.length,2);
    assert.equal(JSON.parse(posts[0].data).force,undefined);
    assert.equal(JSON.parse(posts[1].data).force,true);
    assert.equal(prompts.length,1);
    assert.match(prompts[0],/已经下载过/);
    assert.equal(button.value,'已提交');
});

test('declining repeat confirmation does not create a second Bridge task', async () => {
    const {ctx,requests}=createHarness(req=>respondWithTorrent(req,request=>{
        if(request.method==='GET') {
            request.onload({status:200,responseText:'{"targets":[{"id":"nas"}]}'});
        } else {
            request.onload({status:409,responseText:'{"existing_task_id":"old-task","status":"COMPLETED"}'});
        }
    }));
    ctx.window.confirm=()=>false;
    const doc=fakeDocument();ctx.document=doc;
    const button={tagName:'INPUT',dataset:{},value:'发送到 PikPak',disabled:false,title:''};
    ctx.testButton=button;
    await evaluate(ctx,"sendTorrentToBridge('https://exhentai.org/torrent/123/" + hexHash + "/file.torrent', testButton)");
    assert.equal(requests.filter(x=>x.method==='POST').length,1);
    assert.equal(button.value,'已取消');
});
