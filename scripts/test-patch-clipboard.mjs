import { execFileSync } from 'node:child_process';

const [session, address] = process.argv.slice(2);
const origin = new URL(address);
if (!['127.0.0.1', 'localhost'].includes(origin.hostname)) throw new Error('Clipboard tests only grant access to loopback origins.');
if (typeof WebSocket !== 'function') throw new Error('Clipboard tests require Node.js 22 or later.');

const endpoint = JSON.parse(execFileSync('agent-browser', ['--session', session, 'get', 'cdp-url', '--json'], {encoding: 'utf8'}));
if (!endpoint.success) throw new Error('Cannot access the local test browser.');
const socket = new WebSocket(endpoint.data.cdpUrl);
const pending = new Map();
let sequence = 0;
const timeout = setTimeout(() => {
    console.error('Clipboard permission request timed out.');
    process.exit(1);
}, 10000);
socket.addEventListener('message', event => {
    const response = JSON.parse(event.data);
    const request = pending.get(response.id);
    if (!request) return;
    pending.delete(response.id);
    if (response.error) request.reject(new Error(response.error.message));
    else request.resolve(response.result);
});
function send(method, params = {}) {
    const id = ++sequence;
    return new Promise((resolve, reject) => {
        pending.set(id, {resolve, reject});
        socket.send(JSON.stringify({id, method, params}));
    });
}
try {
    await new Promise((resolve, reject) => {
        socket.addEventListener('open', resolve, {once: true});
        socket.addEventListener('error', reject, {once: true});
    });
    const {browserContextIds} = await send('Target.getBrowserContexts');
    for (const browserContextId of [undefined, ...browserContextIds]) {
        for (const name of ['clipboard-read', 'clipboard-write']) {
            await send('Browser.setPermission', {
                origin: origin.origin,
                permission: {name},
                setting: 'granted',
                ...(browserContextId ? {browserContextId} : {}),
            });
        }
    }
    // Permission overrides remain active while this connection is open.
    const browser = (...args) => execFileSync('agent-browser', ['--session', session, ...args], {encoding: 'utf8'});
    browser('scrollintoview', '#copy-patch');
    browser('click', '#copy-patch');
    browser('wait', '--fn', 'document.getElementById("draft-status").textContent === "Patch copied."');
    browser('eval', '(async()=>{ const copied = await navigator.clipboard.readText(); if (copied !== document.getElementById("config-diff").textContent) throw new Error("Clipboard text differs from the patch preview"); })()');
} finally {
    clearTimeout(timeout);
    socket.close();
}
