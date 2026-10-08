'use strict';

const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const script = fs.readFileSync(path.join(__dirname, 'eharia2-pikpak-bridge.user.js'), 'utf8');
const hexHash = '0123456789abcdef0123456789abcdef01234567';

function createHarness(respond) {
    const requests = [];
    const config = {
        ARIA2_RPC: '',
        BRIDGE_URL: 'https://bridge.example.test/base/',
        BRIDGE_TARGET: 'movies'
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
        window: {location: {
            host: 'e-hentai.org', href: 'https://e-hentai.org/g/123/abc/',
            pathname: '/g/123/abc/'
        }},
        GM_config: StubGMConfig,
        GM_registerMenuCommand() {},
        GM_getValue() { return null; },
        GM_setValue() {},
        GM_xmlhttpRequest(req) {
            requests.push(req);
            respond(req);
        },
        setTimeout,
        clearTimeout,
        console: {log() {}, warn() {}, error() {}},
        alert() {}
    };
    vm.createContext(ctx);
    vm.runInContext(script, ctx, {filename: 'eharia2-pikpak-bridge.user.js'});
    return {ctx, requests, config};
}

function evaluate(ctx, expression) {
    return vm.runInContext(expression, ctx);
}

test('upstream torrent URL converts to 40-character BTIH magnet', () => {
    const {ctx} = createHarness(() => {});
    const magnet = evaluate(ctx, "bridgeMagnetFromTorrentLink('https://exhentai.org/torrent/123/" + hexHash + "/file.torrent')");
    assert.match(magnet, /^magnet:\?xt=urn:btih:/);
    assert.match(magnet, /tr=http%3A%2F%2Fehtracker\.org%2F123%2Fannounce$/);
    assert.throws(() => evaluate(ctx, "bridgeMagnetFromTorrentLink('https://example.org/file.torrent')"), /BTIH/);
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

test('torrent page and popup preserve aria2 and add separate Bridge entries', () => {
    assert.match(script, /this\.bridgeButton\.onclick = \(\) => sendTorrentToBridge\(this\.link, this\.bridgeButton\)/);
    assert.match(script, /const bridgeButton = event\.target\.closest/);
    assert.match(script, /ariaClient\.addUri\(getTorrentLink\(link\), gmc\.get\('ARIA2_DIR'\)\)/);
    assert.match(script, /class="aria2helper-one-click bt-bridge-button bt"/);
    assert.match(script, /@connect\s+\*/);
});
