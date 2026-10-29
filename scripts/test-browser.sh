#!/usr/bin/env bash
set -euo pipefail

# Tests use the running application and its configured Alertmanager.
URL=${ART_BROWSER_URL:-http://127.0.0.1:18080}
SESSION=${ART_BROWSER_SESSION:-art-issues}
ab() { agent-browser --session "$SESSION" "$@"; }
ab open "$URL"

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
