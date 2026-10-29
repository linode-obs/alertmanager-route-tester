#!/usr/bin/env bash
set -euo pipefail

URL=${ART_BROWSER_URL:?Run through scripts/run-browser-tests.sh screenshots}
SESSION=art-readme
OUTPUT=${ART_SCREENSHOT_DIR:-docs/screenshots}
ab() {
    if [[ ${1:-} == click ]]; then agent-browser --session "$SESSION" scrollintoview "$2"; fi
    agent-browser --session "$SESSION" "$@"
}
trap 'agent-browser --session "$SESSION" close' EXIT
ab open "$URL/?alertmanager=local"
ab set viewport 1440 1100
ab eval --stdin <<'JS'
(() => {
    const expected = ['demo-inbox', 'demo-sandbox-chat', 'demo-checkout-oncall', 'demo-checkout-chat', 'demo-database-oncall'].sort();
    const actual = Array.from(document.getElementById('target-receiver').options, option => option.value).filter(Boolean).sort();
    const connection = new URL(document.querySelector('.am-link').href);
    if (connection.hostname !== '127.0.0.1' || JSON.stringify(actual) !== JSON.stringify(expected)) throw new Error('Screenshots require the isolated fictional demo configuration');
    sessionStorage.clear();
    currentLabels = {};
    renderLabels();
    clearRecentLabels();
    setTheme('light');
})()
JS
for label in 'alertname=CheckoutLatencyHigh' 'service=checkout' 'severity=critical' 'environment=sandbox'; do
    ab fill '#label-key-input' "${label%%=*}"
    ab fill '#label-value-input' "${label#*=}"
    ab click '.btn-add-label'
done
ab click '#routing-options > summary'
ab fill '#receiver-search' 'chk oncl'
ab wait '#receiver-options [role=option]'
ab press Enter
ab click '.btn-test-route'
ab wait '.receiver-check-summary'
ab eval 'if (document.querySelectorAll(".receiver-check-summary").length !== 1 || !document.querySelector(".receiver-check-summary").textContent.includes("demo-sandbox-chat")) throw new Error("Demo receiver explanation is not ready");'
ab select '#recent-labels' 0
ab fill '#receiver-search' 'chk oncl'
ab eval 'document.querySelector(".workspace").scrollIntoView({block:"start"});'
ab eval '(async()=>{await Promise.all(document.getAnimations().map(animation=>animation.finished.catch(()=>{})));})()'
mkdir -p "$OUTPUT"
ab screenshot "$OUTPUT/receiver-search-demo.png"
ab press Enter
ab click '#start-draft'
ab wait '#draft-editor[open]'
ab eval --stdin <<'JS'
(() => {
    const editor = document.getElementById('config-draft');
    if (!editor.value.includes('environment="sandbox"')) throw new Error('Expected demo matcher is absent');
    editor.value = editor.value.replace('environment="sandbox"', 'environment="staging"');
    editor.dispatchEvent(new Event('input'));
})()
JS
ab fill '#patch-target' config/alertmanager.yml
ab click '#preview-draft'
ab wait --fn 'document.getElementById("config-diff").textContent.includes("diff --git") && document.getElementById("config-diff").textContent.includes("staging")'
ab eval --stdin <<'JS'
(async () => {
    const editor = document.getElementById('config-draft');
    const line = editor.value.slice(0, editor.value.indexOf('\nroute:\n') + 1).split('\n').length - 1;
    const style = getComputedStyle(editor);
    editor.scrollTop = Math.max(0, line * parseFloat(style.lineHeight) + parseFloat(style.paddingTop));
    editor.scrollLeft = 0;
    document.getElementById('draft-editor').scrollTop = 0;
    await Promise.all(document.getAnimations().map(animation => animation.finished.catch(() => {})));
})()
JS
ab screenshot "$OUTPUT/draft-patch-demo.png"
ab click '#discard-draft'
printf 'Synthetic screenshots saved to %s\n' "$OUTPUT"
