#!/usr/bin/env bash
set -euo pipefail

# Tests use the running application and its configured Alertmanager.
URL=${ART_BROWSER_URL:-http://127.0.0.1:18080}
SESSION=${ART_BROWSER_SESSION:-art-issues-$$}
ab() {
    if [[ ${1:-} == click ]]; then agent-browser --session "$SESSION" scrollintoview "$2"; fi
    agent-browser --session "$SESSION" "$@"
}
cleanup_browser() {
    status=$?
    if [[ $status == 0 ]]; then ab close; else echo "Browser test session retained: $SESSION" >&2; fi
}
trap cleanup_browser EXIT
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
    for (const selector of ['#recent-labels', '#receiver-search', '#start-draft']) {
        const control = document.querySelector(selector);
        if (!options.contains(control) || control.checkVisibility()) throw new Error('Secondary control is visible before More options opens');
    }
    if (options.querySelectorAll('.help-tip[tabindex="0"][aria-label]').length < 4) throw new Error('Secondary controls lack accessible explanations');
})()
JS
ab click '#routing-options > summary'

if [[ ${1:-all} == all || ${1:-all} == capture-guard ]]; then
GUARD=$(mktemp -d)
if ART_SCREENSHOT_DIR="$GUARD" bash scripts/capture-readme-screenshots.sh > "$GUARD/output.log" 2>&1; then
    echo "Screenshot capture accepted a configuration other than the fictional demo: $GUARD" >&2
    exit 1
fi
if ! grep -q 'Screenshots require the isolated fictional demo configuration' "$GUARD/output.log" || [[ -n $(find "$GUARD" -name '*.png' -print -quit) ]]; then
    echo "Screenshot capture did not reject the unrelated configuration before saving images: $GUARD" >&2
    exit 1
fi
rm -rf "$GUARD"
fi

if [[ ${1:-all} == all || ${1:-all} == editor ]]; then
ab click '#start-draft'
ab wait --fn 'document.getElementById("config-draft").value.length > 0'
ab eval --stdin <<'JS'
(() => {
    const dialog = document.querySelector('#draft-editor[open]');
    if (!dialog) throw new Error('Draft editor is not expanded into a dialog');
    const editor = document.getElementById('config-draft');
    if (dialog.getBoundingClientRect().width < innerWidth * 0.85) throw new Error('Draft editor is too narrow');
    if (editor.getBoundingClientRect().height < innerHeight * 0.6) throw new Error('Draft editor is too short');
    if (parseFloat(getComputedStyle(editor).fontSize) < 14) throw new Error('Draft text is too small');
    const help = document.getElementById('draft-editor-help');
    const colors = [getComputedStyle(help).color, getComputedStyle(help.closest('header')).backgroundColor].map(color => {
        const channels = color.match(/[\d.]+/g).slice(0,3).map(Number).map(value => value / 255).map(value => value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4);
        return channels[0] * 0.2126 + channels[1] * 0.7152 + channels[2] * 0.0722;
    });
    if ((Math.max(...colors) + 0.05) / (Math.min(...colors) + 0.05) < 4.5) throw new Error('Draft editor help has insufficient contrast');
})()
JS
ab press Escape
ab wait --fn 'document.activeElement.id === "edit-draft"'
ab eval 'if (document.getElementById("draft-editor").open || draftBaseline === null) throw new Error("Closing the editor discarded the draft"); if (document.activeElement.id !== "edit-draft") throw new Error("Closing the draft editor did not return focus to its opener");'
ab click '#edit-draft'
ab wait '#draft-editor[open]'
ab click '#discard-draft'
ab wait --fn 'document.activeElement.id === "start-draft"'
ab eval 'if (document.getElementById("draft-editor").open || draftBaseline !== null) throw new Error("Discard did not close the editor and clear the draft");'
fi

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
ab click '#use-draft'
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
ab click '#edit-draft'
ab click '#discard-draft'
fi

if [[ ${1:-all} == all || ${1:-all} == draft ]]; then
ab eval 'if (!document.getElementById("start-draft")) throw new Error("Configuration drafts are unavailable");'
ab click '#start-draft'
ab wait --fn 'document.getElementById("config-draft").value.includes("pagerduty-critical")'
ab eval 'document.getElementById("config-draft").value = document.getElementById("config-draft").value.replaceAll("pagerduty-critical", "draft-critical"); document.getElementById("config-draft").dispatchEvent(new Event("input"));'
ab click '#use-draft'
ab eval 'if (!document.getElementById("view-draft-patch")) throw new Error("The draft action must open a patch preview, not download a file");'
ab click '#view-draft-patch'
ab wait --fn '/^\+\s*-?\s*receiver: draft-critical$/m.test(document.getElementById("config-diff").textContent)'
ab eval 'if (!document.getElementById("draft-editor").open || document.getElementById("draft-diff").hidden) throw new Error("Patch preview did not open"); if (Math.abs(document.getElementById("config-diff").getBoundingClientRect().top - document.getElementById("config-draft").getBoundingClientRect().top) > 2) throw new Error("Patch and configuration panes are misaligned");'
if [[ -n $(find "$ART_BROWSER_DOWNLOAD_DIR" -type f -print -quit) ]]; then echo "Viewing a patch started a download" >&2; exit 1; fi
ab fill '#patch-target' configs/alertmanager.yml
ab eval 'if (!document.getElementById("copy-patch").disabled || !document.getElementById("download-patch").disabled) throw new Error("Changed patch target left stale actions enabled");'
ab click '#preview-draft'
ab wait --fn 'document.getElementById("config-diff").textContent.includes("a/configs/alertmanager.yml")'
node scripts/test-patch-clipboard.mjs "$SESSION" "$URL"
if [[ -n $(find "$ART_BROWSER_DOWNLOAD_DIR" -type f -print -quit) ]]; then echo "Copying a patch started a download" >&2; exit 1; fi
ab scrollintoview '#download-patch'
ab download '#download-patch' /tmp/art-draft-download.patch
python3 - "$URL" <<'PY'
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import urllib.request

with urllib.request.urlopen(sys.argv[1] + '/config/source?alertmanager=local', timeout=10) as response:
    baseline = json.load(response)['source']
patch = Path('/tmp/art-draft-download.patch').read_bytes()
with tempfile.TemporaryDirectory() as directory:
    target = Path(directory) / 'configs/alertmanager.yml'
    target.parent.mkdir()
    target.write_text(baseline)
    subprocess.run(['git', 'apply', '--check', '-'], cwd=directory, input=patch, check=True)
    subprocess.run(['git', 'apply', '-'], cwd=directory, input=patch, check=True)
    assert 'draft-critical' in target.read_text(), 'Downloaded patch did not apply draft changes'
PY
ab eval 'switchMode("ui"); currentLabels={severity:"critical"}; renderLabels();'
ab click '#use-draft'
ab fill '#receiver-search' draftcritical
ab press Enter
ab eval 'if (document.getElementById("target-receiver").value !== "draft-critical") throw new Error("Receiver search did not use draft receivers");'
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
ab click '#edit-draft'
ab eval --stdin <<'JS'
(() => {
    const editor = document.getElementById('config-draft');
    editor.value = editor.value.replace('receivers:\n', 'receivers:\n- name: removed-in-draft\n');
    editor.dispatchEvent(new Event('input'));
})()
JS
ab click '#preview-draft'
ab wait --fn 'document.getElementById("config-diff").textContent.includes("removed-in-draft")'
ab click '#use-draft'
ab fill '#receiver-search' removed-in-draft
ab press Enter
ab eval 'if (document.getElementById("target-receiver").value !== "removed-in-draft") throw new Error("Draft receiver was not selected before removing it");'
ab click '#edit-draft'
ab eval --stdin <<'JS'
(() => {
    const editor = document.getElementById('config-draft');
    editor.value = editor.value.replace('- name: removed-in-draft\n', '');
    editor.dispatchEvent(new Event('input'));
})()
JS
ab click '#use-draft'
ab eval --stdin <<'JS'
(async () => {
    currentLabels = {severity:'critical'};
    renderLabels();
    const completed = new Promise(resolve => document.getElementById('alert-form').addEventListener('htmx:afterRequest', resolve, {once:true}));
    document.getElementById('alert-form').requestSubmit();
    const result = await completed;
    if (!result.detail.successful || !document.querySelector('#result .result-primary-receiver')) throw new Error('Removing an optional receiver prevented draft route testing');
    if (!document.querySelector('#result .receiver-explanation')?.textContent.includes('No route uses this receiver')) throw new Error('Draft no longer explains the removed receiver choice');
})()
JS
ab click '#edit-draft'
ab click '#discard-draft'
ab click '.btn-test-route'
ab wait --fn 'document.querySelector("#result .result-primary-receiver")?.textContent.includes("pagerduty-critical")'
ab eval 'if (document.querySelector("#result .draft-result")) throw new Error("Discard did not restore live routing");'
fi

if [[ ${1:-all} == all || ${1:-all} == search || ${1:-all} == receiver ]]; then
ab eval 'if (!document.querySelector("#receiver-search[role=combobox]")) throw new Error("Receiver selection is not searchable");'
ab fill '#receiver-search' pdplt
ab wait '#receiver-options [role=option]'
ab eval 'if (document.querySelector("#receiver-options [role=option]").textContent !== "pagerduty-platform") throw new Error("Fuzzy receiver search did not rank the intended receiver");'
ab press ArrowDown
ab press ArrowUp
ab press Enter
ab eval 'if (document.getElementById("target-receiver").value !== "pagerduty-platform" || document.getElementById("receiver-search").getAttribute("aria-expanded") !== "false") throw new Error("Keyboard selection failed");'
ab click '#clear-receiver'
ab fill '#receiver-search' 'not-a-configured-receiver'
ab eval 'if (document.getElementById("target-receiver").value || document.getElementById("receiver-search").validity.valid) throw new Error("Unknown receiver was accepted"); if (document.getElementById("receiver-search").getAttribute("aria-invalid") !== "true") throw new Error("Invalid receiver search is reported as valid to assistive technology");'
ab press Escape
ab click '#clear-receiver'
ab fill '#receiver-search' pdplt
ab click '#receiver-options [role=option]'
ab eval 'if (document.getElementById("target-receiver").value !== "pagerduty-platform") throw new Error("Pointer receiver selection failed");'
fi

if [[ ${1:-all} == all || ${1:-all} == receiver ]]; then
ab eval 'switchMode("ui"); currentLabels = {severity:"warning"}; renderLabels();'
ab fill '#receiver-search' pdplt
ab press Enter
ab click '.btn-test-route'
ab wait '.receiver-explanation'
ab eval --stdin <<'JS'
const explanation = document.querySelector('.receiver-explanation');
const text = explanation.textContent;
if (!explanation.querySelector('.receiver-check-summary')) throw new Error('Receiver explanation lacks a clear summary');
if (!text.includes('pagerduty-platform') || !text.includes('service') || !text.includes('Missing') || !text.includes('warning')) throw new Error('Receiver explanation omitted failed parent or child conditions');
if (!explanation.querySelector('.receiver-condition-table')) throw new Error('Receiver conditions are not shown as a comparison');
if (explanation.textContent.includes('root/')) throw new Error('Receiver explanation exposes internal route identifiers');
if (explanation.querySelectorAll('.receiver-check-summary').length !== 1) throw new Error('Receiver explanation repeats its summary');
if (explanation.querySelector('.receiver-blocker-name') && Array.from(explanation.querySelectorAll('.receiver-path-details')).some(details => details.open)) throw new Error('Extra label checks must stay collapsed when an earlier route stopped matching');
if (!(document.querySelector('.result-primary-outcome').compareDocumentPosition(explanation) & Node.DOCUMENT_POSITION_FOLLOWING)) throw new Error('Receiver explanation obscures the main routing result');
JS
ab click '#clear-receiver'
fi

if [[ ${1:-all} == all || ${1:-all} == reuse ]]; then
ab eval 'if (!document.getElementById("recent-labels")) throw new Error("Recent label reuse is unavailable");'
ab eval 'switchMode("ui"); currentLabels = {severity:"critical", alertname:"HistoryExample"}; renderLabels();'
ab click '.btn-test-route'
ab wait '.result-primary-receiver'
ab wait --fn 'document.getElementById("recent-labels").options[1]?.textContent.includes("HistoryExample")'
ab eval 'currentLabels = {}; renderLabels();'
ab select '#recent-labels' 0
ab eval 'if (Object.keys(currentLabels).length) throw new Error("Selecting a recent test must not load it automatically");'
ab eval --stdin <<'JS'
(async () => {
    const done = new Promise(resolve => document.getElementById('alert-form').addEventListener('htmx:afterRequest', resolve, {once:true}));
    document.getElementById('alert-form').requestSubmit();
    await done;
    if (!document.getElementById('recent-labels').selectedOptions[0].textContent.includes('HistoryExample')) throw new Error('A routing result reset the selected recent test');
})()
JS
ab click '#load-recent-labels'
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
ab eval 'window.labelsBeforeHistoryClear = JSON.stringify(currentLabels);'
ab click '#clear-recent-labels'
ab eval 'if (document.getElementById("recent-labels").options.length !== 1) throw new Error("Recent labels were not cleared"); if (JSON.stringify(currentLabels) !== window.labelsBeforeHistoryClear) throw new Error("Clearing history changed current labels"); if (document.getElementById("clear-recent-labels").textContent !== "Clear history") throw new Error("Clear history action is ambiguous");'
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
