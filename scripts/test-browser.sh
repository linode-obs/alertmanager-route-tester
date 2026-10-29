#!/usr/bin/env bash
set -euo pipefail

# Tests use the running application and its configured Alertmanager.
URL=${ART_BROWSER_URL:-http://127.0.0.1:18080}
SESSION=${ART_BROWSER_SESSION:-art-issues}
ab() { agent-browser --session "$SESSION" "$@"; }
ab open "$URL"
ab set viewport 1440 1000
ab eval --stdin <<'JS'
(() => {
    const options = document.getElementById('routing-options');
    if (!options || options.open) throw new Error('Secondary controls must be collapsed initially');
    const cards = document.querySelectorAll('.sample-alert-card');
    if (cards.length > 4) throw new Error('More than four quick examples are visible');
    for (const card of cards) {
        if (card.querySelector('.sample-name').textContent.length > 40 || card.querySelector('.sample-description').textContent.length > 40) throw new Error('Quick example copy is too long');
    }
    for (const selector of ['#recent-labels', '#target-receiver', '#start-draft']) {
        const control = document.querySelector(selector);
        if (!options.contains(control) || control.checkVisibility()) throw new Error('Secondary control is visible before More options opens');
    }
    if (options.querySelectorAll('.help-tip[tabindex="0"][aria-label]').length < 4) throw new Error('Secondary controls lack accessible explanations');
})()
JS
ab click '#routing-options > summary'

if [[ ${1:-all} == all || ${1:-all} == labels ]]; then
ab eval --stdin <<'JS'
(async () => {
    switchMode('ui');
    currentLabels = {};
    document.getElementById('label-key-input').value = '__proto__';
    document.getElementById('label-value-input').value = 'prototype-label';
    addLabel();
    if (!Object.hasOwn(currentLabels, '__proto__')) throw new Error('Label editor discarded __proto__');
    const shared = decodeSharedLabels(encodeSharedLabels(currentLabels));
    if (shared['__proto__'] !== 'prototype-label') throw new Error('Restoring labels discarded __proto__');
    document.getElementById('label-key-input').value = 'constructor';
    renderValueSuggestions();
    currentLabels = {alertmanager:'label-instance', target_receiver:'label-receiver', draft:'label-draft', empty:''};
    renderLabels();
    const done = new Promise(resolve => document.getElementById('alert-form').addEventListener('htmx:afterRequest', resolve, {once:true}));
    document.getElementById('alert-form').requestSubmit();
    const event = await done;
    if (!event.detail.successful) throw new Error('Label names collided with routing controls');
    const labels = document.querySelector('#result .alert-labels');
    for (const [name, value] of Object.entries(currentLabels)) {
        if (!Array.from(labels.querySelectorAll('.label-item')).some(item => item.querySelector('.label-key').textContent === name && item.querySelector('.label-value').textContent === value)) throw new Error(`Routing discarded label ${name}`);
    }
    switchMode('raw');
    document.getElementById('raw-input').value += '\npending: yaml';
    switchMode('raw');
    if (!document.getElementById('raw-input').value.includes('pending: yaml')) throw new Error('Clicking the active YAML mode discarded edits');
    switchMode('ui');
})()
JS
fi

if [[ ${1:-all} == all || ${1:-all} == visual ]]; then
ab click '#start-draft'
ab wait --fn 'document.getElementById("config-draft").value.length > 0'
ab eval --stdin <<'JS'
document.getElementById('config-draft').value = 'route:\n  receiver: default\n  routes:\n  - receiver: hosts\n    match:\n      ccsret_testing_service_endpoint: hosts\nreceivers:\n- name: default\n- name: hosts\n';
switchMode('ui');
currentLabels = {ccsret_testing_service_endpoint:'hosts'};
renderLabels();
JS
ab click '.btn-test-route'
ab wait '.route-matching-details'
ab eval 'document.querySelector(".route-matching-details").open = true;'
for theme in light dark; do
ab eval "setTheme('$theme')"
for width in 1440 1024 768; do
ab set viewport "$width" 1000
ab eval --stdin <<'JS'
(() => {
const trace = document.querySelector('.route-trace');
if (document.documentElement.scrollWidth > window.innerWidth + 1) throw new Error('Route result overflows viewport');
for (const name of document.querySelectorAll('.sample-name')) {
    if (name.scrollWidth > name.clientWidth + 1) throw new Error('Quick example title overflows its card');
}
function luminance(color) {
    const channels = color.match(/[\d.]+/g).slice(0,3).map(Number).map(value => value / 255).map(value => value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4);
    return channels[0] * 0.2126 + channels[1] * 0.7152 + channels[2] * 0.0722;
}
for (const element of trace.querySelectorAll('.trace-title, .trace-kv')) {
    let background = element;
    while (background.parentElement && ['transparent', 'rgba(0, 0, 0, 0)'].includes(getComputedStyle(background).backgroundColor)) background = background.parentElement;
    const foreground = luminance(getComputedStyle(element).color);
    const surface = luminance(getComputedStyle(background).backgroundColor);
    if ((Math.max(foreground, surface) + 0.05) / (Math.min(foreground, surface) + 0.05) < 4.5) throw new Error('Route text contrast is below 4.5:1');
}
const title = trace.querySelector('.trace-title');
if (parseFloat(getComputedStyle(title).fontSize) < 12) throw new Error('Route headings are too small to read');
const label = trace.querySelector('.matcher-table tbody td code');
if (label.getClientRects().length > 1) throw new Error('Label name breaks inside a word');
const table = trace.querySelector('.matcher-table');
if (table.scrollWidth > trace.clientWidth + 1) throw new Error('Matched-label table exceeds route ladder width');
})()
JS
ab scrollintoview '.route-trace'
ab screenshot "/tmp/art-route-ladder-${theme}-${width}.png"
done
done
ab set viewport 1440 1000
ab click '#discard-draft'
fi

if [[ ${1:-all} == all || ${1:-all} == draft ]]; then
ab eval 'if (!document.getElementById("start-draft")) throw new Error("Configuration drafts are unavailable");'
ab click '#start-draft'
ab wait --fn 'document.getElementById("config-draft").value.includes("pagerduty-critical")'
ab eval 'document.getElementById("config-draft").value = document.getElementById("config-draft").value.replaceAll("pagerduty-critical", "draft-critical"); document.getElementById("config-draft").dispatchEvent(new Event("input"));'
ab click '#preview-draft'
ab wait --fn '/^\+\s*-?\s*receiver: draft-critical$/m.test(document.getElementById("config-diff").textContent)'
ab eval 'switchMode("ui"); currentLabels={severity:"critical"}; renderLabels();'
ab click '.btn-test-route'
ab wait --fn 'document.querySelector("#result .result-primary-receiver")?.textContent.includes("draft-critical")'
ab eval 'if (!document.querySelector("#result .draft-result")) throw new Error("Draft results are not identified"); if (Array.from({length:sessionStorage.length},(_,i)=>sessionStorage.getItem(sessionStorage.key(i))).some(value => value.includes("draft-critical"))) throw new Error("Draft configuration entered browser storage");'
ab eval --stdin <<'JS'
(async () => {
    const response = await fetch('/test', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({labels:{severity:'critical'},alertmanager:'local'})});
    const result = await response.json();
    if (result.receiver !== 'pagerduty-critical' || result.draft) throw new Error('Draft changed cached live routing');
    const invalid = await fetch('/config/draft', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({alertmanager:'local',baseline:draftBaseline,draft:'route: ['})});
    if (invalid.status !== 400) throw new Error('Invalid draft was accepted');
})()
JS
ab click '#discard-draft'
ab click '.btn-test-route'
ab wait --fn 'document.querySelector("#result .result-primary-receiver")?.textContent.includes("pagerduty-critical")'
ab eval 'if (document.querySelector("#result .draft-result")) throw new Error("Discard did not restore live routing");'
fi

if [[ ${1:-all} == all || ${1:-all} == receiver ]]; then
ab eval 'switchMode("ui"); currentLabels = {severity:"warning"}; renderLabels();'
ab select '#target-receiver' pagerduty-platform
ab click '.btn-test-route'
ab wait '.receiver-explanation'
ab eval --stdin <<'JS'
const text = document.querySelector('.receiver-explanation').textContent;
if (!text.includes('pagerduty-platform') || !text.includes('service') || !text.includes('label is missing') || !text.includes('warning')) throw new Error('Receiver explanation omitted failed parent or child conditions');
JS
ab select '#target-receiver' ''
fi

if [[ ${1:-all} == all || ${1:-all} == reuse ]]; then
ab eval 'if (!document.getElementById("recent-labels")) throw new Error("Recent label reuse is unavailable");'
ab eval 'switchMode("ui"); currentLabels = {severity:"critical", alertname:"HistoryExample"}; renderLabels();'
ab click '.btn-test-route'
ab wait '.result-primary-receiver'
ab wait --fn 'document.getElementById("recent-labels").options.length > 1'
ab eval 'currentLabels = {}; renderLabels();'
ab select '#recent-labels' 0
ab eval 'if (currentLabels.alertname !== "HistoryExample") throw new Error("Recent labels did not load");'
ab eval --stdin <<'JS'
(async () => {
    for (let index = 0; index < 12; index++) {
        currentLabels = {severity:'critical', alertname:`History${index}`};
        renderLabels();
        const done = new Promise(resolve => document.getElementById('alert-form').addEventListener('htmx:afterRequest', resolve, {once:true}));
        document.getElementById('alert-form').requestSubmit();
        await done;
    }
    if (document.getElementById('recent-labels').options.length !== 11) throw new Error('Recent labels are not limited to ten');
    const done = new Promise(resolve => document.getElementById('alert-form').addEventListener('htmx:afterRequest', resolve, {once:true}));
    document.getElementById('alert-form').requestSubmit();
    await done;
    if (document.getElementById('recent-labels').options.length !== 11) throw new Error('Duplicate labels entered history');
})()
JS
ab click '#clear-recent-labels'
ab eval 'if (document.getElementById("recent-labels").options.length !== 1) throw new Error("Recent labels were not cleared");'
ab eval --stdin <<'JS'
(async () => {
    const response = await fetch('/labels/suggestions', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({labels:{team:'database'},alertmanager:'local'})});
    if (!response.ok) throw new Error(`Suggestions returned ${response.status}`);
    const suggestions = await response.json();
    if (!suggestions.some(item => item.key === 'alertname')) throw new Error('Nested route suggestions are missing');
    return 'Label reuse and suggestion tests passed';
})()
JS
fi

if [[ ${1:-all} == all || ${1:-all} == yaml ]]; then
ab eval --stdin <<'JS'
(async () => {
    currentLabels = {severity: 'critical', message: 'a: b\n"quoted"\\value', empty: '', 'ü:name': '日本語'};
    renderLabels();
    switchMode('raw');
    const yaml = document.getElementById('raw-input').value;
    if (!yaml.includes('critical')) throw new Error('Switching to YAML discarded current labels');
    const response = await fetch('/labels/parse', {method: 'POST', body: yaml});
    if (!response.ok) throw new Error(`Label import returned ${response.status}`);
    const parsed = await response.json();
    if (JSON.stringify(parsed.labels) !== JSON.stringify(currentLabels)) {
        for (const [name, value] of Object.entries(currentLabels)) {
            if (parsed.labels[name] !== value) throw new Error(`YAML round trip changed ${name}`);
        }
        if (Object.keys(parsed.labels).length !== Object.keys(currentLabels).length) throw new Error('YAML round trip changed label count');
    }
    for (const invalid of ['a: [b]', 'a: null', 'a: x\na: y', 'a: x\n---\nb: y', '[a, b]', '"": value']) {
        const result = await fetch('/labels/parse', {method: 'POST', body: invalid});
        if (result.status !== 400) throw new Error(`Invalid YAML returned ${result.status}: ${invalid}`);
    }
    const oversized = await fetch('/labels/parse', {method: 'POST', body: 'a: ' + 'x'.repeat(1 << 20)});
    if (oversized.status !== 413) throw new Error('Oversized YAML was accepted');
    await parseRawInput();
    if (document.getElementById('raw-builder').style.display !== 'none') throw new Error('YAML import did not return to the label editor');
    if (currentLabels.message !== 'a: b\n"quoted"\\value' || currentLabels.empty !== '') throw new Error('YAML import changed values');
    return 'YAML label tests passed';
})()
JS
fi

if [[ ${1:-all} == all || ${1:-all} == reload ]]; then
ab eval --stdin <<'JS'
currentLabels = {severity: 'critical', note: 'keep me'};
renderLabels();
document.getElementById('label-key-input').value = 'unfinished';
document.getElementById('label-value-input').value = 'value';
switchMode('raw');
document.getElementById('raw-input').value += '\npending: yaml';
JS
ab click '.btn-reload-config'
ab wait --load networkidle
ab eval --stdin <<'JS'
if (currentLabels.note !== 'keep me' || currentLabels.severity !== 'critical') throw new Error('Config reload discarded labels');
if (!document.getElementById('raw-input').value.includes('pending: yaml')) throw new Error('Config reload discarded raw input');
if (document.getElementById('raw-builder').style.display === 'none') throw new Error('Config reload changed editor mode');
if (document.getElementById('label-key-input').value !== 'unfinished' || document.getElementById('label-value-input').value !== 'value') throw new Error('Config reload discarded unfinished label');
JS
ab select '#alertmanager-select' second
ab wait --load networkidle
ab eval 'if (Object.keys(currentLabels).length) throw new Error("Labels crossed Alertmanager instances");'
ab select '#alertmanager-select' local
ab wait --load networkidle
ab eval 'if (currentLabels.note !== "keep me") throw new Error("Returning to the instance lost its labels");'
ab eval 'window.location.hash = "labels=" + encodeSharedLabels({severity:"warning"});'
ab reload
ab wait --load networkidle
ab eval 'currentLabels.note = "edited shared labels"; renderLabels();'
ab click '.btn-reload-config'
ab wait --load networkidle
ab eval 'if (currentLabels.note !== "edited shared labels") throw new Error("Reload restored the share link instead of edited labels"); history.replaceState(null,"",window.location.pathname); "Reload tests passed";'
fi
