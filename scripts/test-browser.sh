#!/usr/bin/env bash
set -euo pipefail

# Tests use the running application and its configured Alertmanager.
URL=${ART_BROWSER_URL:-http://127.0.0.1:18080}
SESSION=${ART_BROWSER_SESSION:-art-issues}
ab() { agent-browser --session "$SESSION" "$@"; }
ab open "$URL"

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
ab eval 'if (currentLabels.note !== "keep me") throw new Error("Returning to the instance lost its labels"); "Reload tests passed";'
fi
